package chat_completions

import (
	"testing"

	"github.com/tidwall/gjson"
)

// Responses streams announce a url_citation after the text it covers. Its
// indexes are relative to its own content part, so the chat offset must be the
// part's starting position, not the total text emitted so far.
func TestConvertCodexResponseToOpenAIStreamAnnotationAfterItsText(t *testing.T) {
	var param any
	send := func(event string) []byte {
		t.Helper()
		out := ConvertCodexResponseToOpenAI(t.Context(), "gpt-5.5", nil, nil, []byte("data: "+event), &param)
		if len(out) != 1 {
			t.Fatalf("event %s produced %d chunks, want 1", event, len(out))
		}
		return out[0]
	}

	send(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"hello"}`)
	first := send(`{"type":"response.output_text.annotation.added","item_id":"msg_1","output_index":0,"content_index":0,"annotation_index":0,"annotation":{"type":"url_citation","url":"https://example.com/a","title":"A","start_index":0,"end_index":5}}`)
	if start, end := gjson.GetBytes(first, "choices.0.delta.annotations.0.start_index").Int(), gjson.GetBytes(first, "choices.0.delta.annotations.0.end_index").Int(); start != 0 || end != 5 {
		t.Fatalf("first annotation = [%d,%d), want [0,5); chunk=%s", start, end, first)
	}

	send(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":1,"delta":" world"}`)
	second := send(`{"type":"response.output_text.annotation.added","item_id":"msg_1","output_index":0,"content_index":1,"annotation_index":0,"annotation":{"type":"url_citation","url":"https://example.com/b","title":"B","start_index":1,"end_index":6}}`)
	if start, end := gjson.GetBytes(second, "choices.0.delta.annotations.0.start_index").Int(), gjson.GetBytes(second, "choices.0.delta.annotations.0.end_index").Int(); start != 6 || end != 11 {
		t.Fatalf("second annotation = [%d,%d), want [6,11); chunk=%s", start, end, second)
	}
}

// When a part's annotations only arrive with the finished message item, each
// part keeps its own starting offset.
func TestConvertCodexResponseToOpenAIStreamItemDoneAnnotationsUsePartOffsets(t *testing.T) {
	var param any
	for _, event := range []string{
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"hello"}`,
		`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":1,"delta":" world"}`,
	} {
		if out := ConvertCodexResponseToOpenAI(t.Context(), "gpt-5.5", nil, nil, []byte("data: "+event), &param); len(out) != 1 {
			t.Fatalf("delta produced %d chunks, want 1", len(out))
		}
	}
	done := `{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_1","type":"message","role":"assistant","content":[` +
		`{"type":"output_text","text":"hello","annotations":[{"type":"url_citation","url":"https://example.com/a","title":"A","start_index":0,"end_index":5}]},` +
		`{"type":"output_text","text":" world","annotations":[{"type":"url_citation","url":"https://example.com/b","title":"B","start_index":1,"end_index":6}]}]}}`
	out := ConvertCodexResponseToOpenAI(t.Context(), "gpt-5.5", nil, nil, []byte("data: "+done), &param)
	if len(out) != 1 {
		t.Fatalf("output_item.done produced %d chunks, want 1", len(out))
	}
	annotations := gjson.GetBytes(out[0], "choices.0.delta.annotations").Array()
	if len(annotations) != 2 {
		t.Fatalf("annotations = %s, want 2", gjson.GetBytes(out[0], "choices.0.delta.annotations").Raw)
	}
	for i, want := range [][2]int64{{0, 5}, {6, 11}} {
		if start, end := annotations[i].Get("start_index").Int(), annotations[i].Get("end_index").Int(); start != want[0] || end != want[1] {
			t.Fatalf("annotation %d = [%d,%d), want [%d,%d); chunk=%s", i, start, end, want[0], want[1], out[0])
		}
	}
}
