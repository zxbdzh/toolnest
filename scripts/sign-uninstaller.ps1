#requires -Version 7.2
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Path)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
# NSIS 的 !uninstfinalize 在嵌入前调用；任何失败必须返回非零中止编译。
try {
    if (!$env:TOOLNEST_SIGNTOOL -or !$env:TOOLNEST_CERT_THUMBPRINT -or !$env:TOOLNEST_TIMESTAMP_URL) { throw 'External uninstaller signing credentials missing.' }
    if (!(Test-Path -LiteralPath $Path -PathType Leaf)) { throw 'NSIS uninstaller image missing.' }
    & $env:TOOLNEST_SIGNTOOL sign /sha1 $env:TOOLNEST_CERT_THUMBPRINT /fd SHA256 /tr $env:TOOLNEST_TIMESTAMP_URL /td SHA256 $Path
    if ($LASTEXITCODE) { throw 'Uninstaller signing failed.' }
    & $env:TOOLNEST_SIGNTOOL verify /pa /all $Path
    if ($LASTEXITCODE -or (Get-AuthenticodeSignature -LiteralPath $Path).Status -ne 'Valid') { throw 'Uninstaller signature invalid.' }
} catch { Write-Error $_; exit 1 }
