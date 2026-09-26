# Cross-compiles sipline into dist/ for Windows, Linux and macOS.
# Run: powershell -ExecutionPolicy Bypass -File build.ps1
$ErrorActionPreference = 'Stop'
$saved = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
try {
    $env:CGO_ENABLED = '0'
    foreach ($os in 'windows', 'linux', 'darwin') {
        foreach ($arch in 'amd64', 'arm64') {
            $env:GOOS, $env:GOARCH = $os, $arch
            $out = "dist/sipline-$os-$arch"
            if ($os -eq 'windows') { $out += '.exe' }
            go build -trimpath -ldflags '-s -w' -o $out .
            if ($LASTEXITCODE) { exit $LASTEXITCODE }
            Write-Host $out
        }
    }
} finally {
    $env:GOOS, $env:GOARCH, $env:CGO_ENABLED = $saved
}
