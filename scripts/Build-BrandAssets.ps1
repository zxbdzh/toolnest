#requires -Version 7.2
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$Output,
    [string]$Go='go.exe',
    [string]$ResourceTool='rsrc.exe'
)
$ErrorActionPreference='Stop'
if($env:TOOLNEST_RESOURCE_TOOL){$ResourceTool=$env:TOOLNEST_RESOURCE_TOOL}
$root=Split-Path $PSScriptRoot -Parent
$Output=[IO.Path]::GetFullPath($Output)
if(Test-Path -LiteralPath $Output){throw 'Brand assets require a new directory.'}
if(!(Test-Path -LiteralPath $ResourceTool -PathType Leaf)){throw 'Install pinned resource builder: go install github.com/akavel/rsrc@v0.10.2 into an external GOBIN.'}
$identity=& $Go version -m $ResourceTool
if($LASTEXITCODE-or ($identity-join "`n")-notmatch 'github.com/akavel/rsrc\s+v0\.10\.2'){throw 'Pinned rsrc v0.10.2 required.'}
Push-Location $root
try{
    & $Go run -mod=readonly ./scripts/icons --out $Output
    if($LASTEXITCODE){throw 'Icon generation failed.'}
    $resource=Join-Path $Output 'brand_windows_amd64.syso'
    & $ResourceTool -arch amd64 -ico (Join-Path $Output 'ToolNest.ico') -o $resource
    if($LASTEXITCODE){throw 'Windows icon resource generation failed.'}
    # 可复现生成物供 Go 链接器读取；源图形留在 brand 包，不把外部工具引入运行依赖。
    Copy-Item -LiteralPath $resource -Destination (Join-Path $root 'internal\brand\brand_windows_amd64.syso')
}finally{Pop-Location}
