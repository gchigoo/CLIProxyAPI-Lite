$ErrorActionPreference = "Stop"

$commit = (git rev-parse --short HEAD)
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
$version = "lite-$commit"
$buildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

docker compose build --build-arg "VERSION=$version" --build-arg "COMMIT=$commit" --build-arg "BUILD_DATE=$buildDate"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
docker compose up -d --pull never
exit $LASTEXITCODE
