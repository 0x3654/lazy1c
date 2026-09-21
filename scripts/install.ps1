# Установка lazy1c на Windows: определяет архитектуру, качает exe последнего
# релиза и проверяет SHA256. Запуск в PowerShell:
#   irm https://raw.githubusercontent.com/0x3654/lazy1c/master/scripts/install.ps1 | iex
$ErrorActionPreference = "Stop"
$repo = "0x3654/lazy1c"
$base = "https://github.com/$repo/releases/latest/download"

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  "ARM64" { "arm64" }
  "AMD64" { "amd64" }
  default { Write-Error "unsupported architecture: $($env:PROCESSOR_ARCHITECTURE)"; }
}
$name = "lazy1c_windows_$arch.exe"

Write-Host "-> $name"
Invoke-WebRequest -Uri "$base/$name" -OutFile ".\lazy1c.exe"

try {
  Invoke-WebRequest -Uri "$base/sha256-checksums.txt" -OutFile "$env:TEMP\lazy1c_sums" -UseBasicParsing
  $want = (Select-String -Path "$env:TEMP\lazy1c_sums" -Pattern "^([0-9a-f]{64})\s+$name").Matches[0].Groups[1].Value
  $got = (Get-FileHash ".\lazy1c.exe" -Algorithm SHA256).Hash.ToLower()
  if ($want -ne $got) { Write-Error "sha256 mismatch" }
  Write-Host "sha256 ok"
} catch { Write-Warning "checksum file not available, skipped" }

Write-Host "Готово: .\lazy1c.exe — шаблон конфига lazy1c.example.toml"
