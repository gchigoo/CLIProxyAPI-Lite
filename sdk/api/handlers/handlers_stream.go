package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	"golang.org/x/net/context"
)

// ExecuteStreamWithAuthManager executes a streaming request via the core auth manager.
// This path is the only supported execution route.
// The returned http.Header carries upstream response headers captured before streaming begins.
func (h *BaseAPIHandler) ExecuteStreamWithAuthManager(ctx context.Context, handlerType, modelName string, rawJSON []byte, alt string) (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage) {
	return h.executeStreamWithAuthManager(ctx, handlerType, modelName, rawJSON, alt, false)
}

// ExecuteImageStreamWithAuthManager executes a streaming OpenAI-compatible image endpoint request.
func (h *BaseAPIHandler) ExecuteImageStreamWithAuthManager(ctx context.Context, handlerType, modelName string, rawJSON []byte, alt string) (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage) {
	return h.executeStreamWithAuthManager(ctx, handlerType, modelName, rawJSON, alt, true)
}

func (h *BaseAPIHandler) executeStreamWithAuthManager(ctx context.Context, handlerType, modelName string, rawJSON []byte, alt string, allowImageModel bool) (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage) {
	return h.executeStreamWithAuthManagerFormats(ctx, handlerType, handlerType, modelName, rawJSON, alt, allowImageModel, modelExecutionOptions{})
}

func (h *BaseAPIHandler) executeStreamWithAuthManagerFormats(ctx context.Context, entryProtocol, exitProtocol, modelName string, rawJSON []byte, alt string, allowImageModel bool, execOptions modelExecutionOptions) (<-chan []byte, http.Header, <-chan *interfaces.ErrorMessage) {
	originalRequestedModel := modelName
	responseProtocol := modelExecutionResponseProtocol(entryProtocol, exitProtocol)
	providers, normalizedModel, errMsg := h.providersForExecution(modelName, originalRequestedModel, allowImageModel, execOptions)
	if errMsg != nil {
		errChan := make(chan *interfaces.ErrorMessage, 1)
		errChan <- errMsg
		close(errChan)
		return nil, nil, errChan
	}
	providers = adjustExecutionProvidersForEntryProtocol(entryProtocol, providers)
	reqMeta := requestExecutionMetadata(ctx)
	reqMeta[coreexecutor.RequestedModelMetadataKey] = originalRequestedModel
	addAuthSelectionModelMetadata(reqMeta, execOptions.AuthSelectionModel)
	addModelExecutionSourceMetadata(reqMeta, execOptions.InternalSource)
	setReasoningEffortMetadata(reqMeta, entryProtocol, normalizedModel, rawJSON)
	setServiceTierMetadata(reqMeta, rawJSON)
	setGenerateMetadata(reqMeta, rawJSON)
	payload := rawJSON
	if len(payload) == 0 {
		payload = nil
	}
	req := coreexecutor.Request{
		Model:   normalizedModel,
		Payload: payload,
	}
	opts := coreexecutor.Options{
		Stream:          true,
		Alt:             alt,
		OriginalRequest: rawJSON,
		SourceFormat:    sdktranslator.FromString(entryProtocol),
		ResponseFormat:  sdktranslator.FromString(responseProtocol),
		Headers:         modelExecutionHeaders(ctx, execOptions.Headers),
		Query:           modelExecutionQuery(ctx, execOptions.Query),
	}
	opts.Metadata = reqMeta
	ctx = enrichContextWithSessionHierarchy(ctx, opts.Headers, req.Payload, opts.Metadata)

	streamResult, err := h.AuthManager.ExecuteStream(ctx, providers, req, opts)
	if err != nil {
		err = enrichAuthSelectionError(err, providers, normalizedModel)
		errMsg := executionErrorMessage(err)
		errChan := make(chan *interfaces.ErrorMessage, 1)
		errChan <- errMsg
		close(errChan)
		return nil, nil, errChan
	}
	if streamResult == nil {
		errMsg := &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: fmt.Errorf("auth manager returned nil stream")}
		errChan := make(chan *interfaces.ErrorMessage, 1)
		errChan <- errMsg
		close(errChan)
		return nil, nil, errChan
	}

	passthroughHeadersEnabled := PassthroughHeadersEnabled(h.Cfg)
	rawStreamHeaders := cloneHeader(streamResult.Headers)
	chunks := streamResult.Chunks
	if chunks == nil {
		closed := make(chan coreexecutor.StreamChunk)
		close(closed)
		chunks = closed
	}
	streamClosedBeforeRead := false
	streamCanceledBeforeRead := false

	var responseSSEValidator *sseJSONValidationState
	if responseProtocol == "openai-response" {
		responseSSEValidator = &sseJSONValidationState{}
	}

	transformStreamPayload := func(payload []byte) ([]byte, bool, *interfaces.ErrorMessage) {
		payload = cloneBytes(payload)
		if responseSSEValidator != nil {
			validatedPayload, errValidate := responseSSEValidator.AddChunk(payload)
			if errValidate != nil {
				return nil, false, &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: errValidate}
			}
			payload = validatedPayload
			if len(payload) == 0 {
				return nil, false, nil
			}
		}
		return payload, true, nil
	}

	var bootstrapPayload []byte
	var bootstrapStreamErr error
	var bootstrapErr *interfaces.ErrorMessage
	readInitialStreamChunks := func() {
		for {
			var chunk coreexecutor.StreamChunk
			var ok bool
			if ctx != nil {
				select {
				case <-ctx.Done():
					streamCanceledBeforeRead = true
					return
				case chunk, ok = <-chunks:
				}
			} else {
				chunk, ok = <-chunks
			}
			if !ok {
				streamClosedBeforeRead = true
				return
			}
			if chunk.Err != nil {
				bootstrapStreamErr = chunk.Err
				return
			}
			if len(chunk.Payload) == 0 {
				continue
			}
			payload, deliverable, errMsg := transformStreamPayload(chunk.Payload)
			if errMsg != nil {
				bootstrapErr = errMsg
				return
			}
			if !deliverable {
				continue
			}
			bootstrapPayload = payload
			return
		}
	}

	bootstrapEligible := func(err error) bool {
		status := statusFromError(err)
		if status == 0 {
			return true
		}
		switch status {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusPaymentRequired,
			http.StatusRequestTimeout, http.StatusTooManyRequests:
			return true
		default:
			return status >= http.StatusInternalServerError
		}
	}

	maxBootstrapRetries := StreamingBootstrapRetries(h.Cfg)
	if h.AuthManager.HomeEnabled() {
		maxBootstrapRetries = 0
	}
	for bootstrapRetries := 0; !streamCanceledBeforeRead; {
		readInitialStreamChunks()
		if streamCanceledBeforeRead || bootstrapErr != nil || bootstrapStreamErr == nil {
			break
		}
		if bootstrapRetries >= maxBootstrapRetries || !bootstrapEligible(bootstrapStreamErr) {
			bootstrapErr = executionErrorMessage(bootstrapStreamErr)
			break
		}
		bootstrapRetries++
		retryResult, retryErr := h.AuthManager.ExecuteStream(ctx, providers, req, opts)
		if retryErr != nil {
			originalBootstrapErr := executionErrorMessage(bootstrapStreamErr)
			if isAuthSelectionUnavailable(retryErr) && originalBootstrapErr.StatusCode >= http.StatusInternalServerError {
				bootstrapErr = originalBootstrapErr
			} else {
				bootstrapErr = executionErrorMessage(enrichAuthSelectionError(retryErr, providers, normalizedModel))
			}
			break
		}
		if retryResult == nil {
			bootstrapErr = executionErrorMessage(fmt.Errorf("auth manager returned nil stream"))
			break
		}
		rawStreamHeaders = cloneHeader(retryResult.Headers)
		streamClosedBeforeRead = false
		bootstrapStreamErr = nil
		bootstrapPayload = nil
		if responseSSEValidator != nil {
			responseSSEValidator = &sseJSONValidationState{}
		}
		chunks = retryResult.Chunks
		if chunks == nil {
			closed := make(chan coreexecutor.StreamChunk)
			close(closed)
			chunks = closed
		}
	}

	upstreamHeaders := downstreamHeadersFromExecutor(rawStreamHeaders, passthroughHeadersEnabled)
	if upstreamHeaders == nil && passthroughHeadersEnabled {
		upstreamHeaders = make(http.Header)
	}
	dataChan := make(chan []byte)
	errChan := make(chan *interfaces.ErrorMessage, 1)

	go func() {
		defer close(dataChan)
		defer close(errChan)
		if streamCanceledBeforeRead {
			return
		}

		sendErr := func(msg *interfaces.ErrorMessage) bool {
			if ctx == nil {
				errChan <- msg
				return true
			}
			select {
			case <-ctx.Done():
				return false
			case errChan <- msg:
				return true
			}
		}

		sendData := func(chunk []byte) bool {
			if ctx == nil {
				dataChan <- chunk
				return true
			}
			select {
			case <-ctx.Done():
				return false
			case dataChan <- chunk:
				return true
			}
		}

		if bootstrapErr != nil {
			_ = sendErr(bootstrapErr)
			return
		}

		if bootstrapPayload != nil {
			if okSendData := sendData(bootstrapPayload); !okSendData {
				return
			}
		}
		for {
			chunk, ok, canceled := nextStreamChunk(ctx, nil, &streamClosedBeforeRead, chunks)
			if canceled {
				return
			}
			if !ok {
				if responseSSEValidator != nil {
					if errValidate := responseSSEValidator.Finish(); errValidate != nil {
						errMsg := &interfaces.ErrorMessage{StatusCode: http.StatusBadGateway, Error: errValidate}
						_ = sendErr(errMsg)
					}
				}
				return
			}
			if chunk.Err != nil {
				errMsg := executionErrorMessage(chunk.Err)
				_ = sendErr(errMsg)
				return
			}
			if len(chunk.Payload) == 0 {
				continue
			}
			payload, deliverable, errMsg := transformStreamPayload(chunk.Payload)
			if errMsg != nil {
				_ = sendErr(errMsg)
				return
			}
			if !deliverable {
				continue
			}
			if okSendData := sendData(payload); !okSendData {
				return
			}
		}
	}()
	return dataChan, upstreamHeaders, errChan
}

func nextStreamChunk(ctx context.Context, pending *[]coreexecutor.StreamChunk, closed *bool, chunks <-chan coreexecutor.StreamChunk) (coreexecutor.StreamChunk, bool, bool) {
	if pending != nil && len(*pending) > 0 {
		chunk := (*pending)[0]
		(*pending)[0] = coreexecutor.StreamChunk{}
		*pending = (*pending)[1:]
		return chunk, true, false
	}
	if closed != nil && *closed {
		return coreexecutor.StreamChunk{}, false, false
	}
	var chunk coreexecutor.StreamChunk
	var ok bool
	if ctx != nil {
		select {
		case <-ctx.Done():
			return coreexecutor.StreamChunk{}, false, true
		case chunk, ok = <-chunks:
		}
	} else {
		chunk, ok = <-chunks
	}
	if !ok && closed != nil {
		*closed = true
	}
	return chunk, ok, false
}

type sseJSONValidationState struct {
	pending        []byte
	pendingErr     error
	prevEndsWithCR bool
}

func (s *sseJSONValidationState) AddChunk(chunk []byte) ([]byte, error) {
	if s.pendingErr != nil {
		errPending := s.pendingErr
		s.pendingErr = nil
		return nil, errPending
	}
	if len(chunk) == 0 {
		return nil, nil
	}
	if s.prevEndsWithCR {
		if chunk[0] == '\n' {
			chunk = chunk[1:]
		}
		s.prevEndsWithCR = false
	}
	if len(chunk) == 0 {
		return nil, nil
	}
	endsWithCR := chunk[len(chunk)-1] == '\r'
	chunk = bytes.ReplaceAll(chunk, []byte("\r\n"), []byte("\n"))
	chunk = bytes.ReplaceAll(chunk, []byte("\r"), []byte("\n"))
	s.prevEndsWithCR = endsWithCR
	if len(s.pending) > 0 && !bytes.HasSuffix(s.pending, []byte("\n")) && !bytes.HasPrefix(chunk, []byte("\n")) {
		first := bytes.TrimSpace(bytes.SplitN(chunk, []byte("\n"), 2)[0])
		if bytes.HasPrefix(first, []byte("data:")) || bytes.HasPrefix(first, []byte("event:")) {
			s.pending = append(s.pending, '\n')
		}
	}
	s.pending = append(s.pending, chunk...)

	var output []byte
	for {
		frameEnd := bytes.Index(s.pending, []byte("\n\n"))
		if frameEnd < 0 {
			break
		}
		frameEnd += 2
		frame := s.pending[:frameEnd]
		if errValidate := validateSSEFrameDataJSON(frame); errValidate != nil {
			if len(output) > 0 {
				s.pending = s.pending[:0]
				s.pendingErr = errValidate
				return output, nil
			}
			return nil, errValidate
		}
		output = append(output, frame...)
		copy(s.pending, s.pending[frameEnd:])
		s.pending = s.pending[:len(s.pending)-frameEnd]
	}

	if len(bytes.TrimSpace(s.pending)) == 0 {
		s.pending = s.pending[:0]
		return output, nil
	}
	payload, found := sseJSONValidationDataPayload(s.pending)
	payload = bytes.TrimSpace(payload)
	if !found || len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) || json.Valid(payload) {
		output = append(output, s.pending...)
		s.pending = s.pending[:0]
	}
	return output, nil
}

func (s *sseJSONValidationState) Finish() error {
	s.prevEndsWithCR = false
	if s.pendingErr != nil {
		errPending := s.pendingErr
		s.pendingErr = nil
		s.pending = nil
		return errPending
	}
	if len(bytes.TrimSpace(s.pending)) == 0 {
		s.pending = nil
		return nil
	}
	errValidate := validateSSEFrameDataJSON(s.pending)
	s.pending = nil
	return errValidate
}

func sseJSONValidationDataPayload(frame []byte) ([]byte, bool) {
	var payload []byte
	found := false
	for _, line := range bytes.Split(frame, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		if found {
			payload = append(payload, '\n')
		}
		payload = append(payload, bytes.TrimSpace(line[len("data:"):])...)
		found = true
	}
	return payload, found
}

func validateSSEFrameDataJSON(frame []byte) error {
	payload, found := sseJSONValidationDataPayload(frame)
	payload = bytes.TrimSpace(payload)
	if !found || len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) || json.Valid(payload) {
		return nil
	}
	const max = 512
	preview := payload
	if len(preview) > max {
		preview = preview[:max]
	}
	return fmt.Errorf("invalid SSE data JSON (len=%d): %q", len(payload), preview)
}

func validateSSEDataJSON(chunk []byte) error {
	state := &sseJSONValidationState{}
	if _, errAdd := state.AddChunk(chunk); errAdd != nil {
		return errAdd
	}
	return state.Finish()
}
