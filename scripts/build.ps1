#requires -Version 7.2
[CmdletBinding()]
param(
    [ValidateSet('format','vet','test','release','verify')][string]$Task = 'test',
    [string]$Go = 'F:/tmp/toolnest-build/go/bin/go.exe',
    [string]$MyGo = 'F:/tmp/toolnest-build/mygo',
    [string]$Output,
    [ValidateSet('.')][string]$MainPackage = '.',
    [string]$Repository,
    [string]$PublicKey,
    [string]$PrivateKeyFile,
    [string]$MakeNSIS,
    [string]$SignTool,
    [string]$CertificateThumbprint,
    [string]$TimestampUrl = 'http://timestamp.digicert.com',
    [switch]$Test,
    [switch]$PublicPreview,
    [switch]$Fix
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if($Test-and $PublicPreview){throw '-Test and -PublicPreview are mutually exclusive.'}
$Root = Split-Path $PSScriptRoot -Parent
$PinnedMyGo = '0d644f4aca24be8208d1b7d364d7c928fc2cb890'
$PinnedGo = 'go1.27.4'
$Version = (Get-Content -LiteralPath (Join-Path $Root 'packaging/VERSION') -Raw).Trim()
if ($Version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') { throw 'packaging/VERSION must be stable x.y.z.' }
if (@($Version.Split('.') | Where-Object { [long]$_ -gt 65535 }).Count) { throw 'NSIS version component exceeds 65535.' }
if ($Fix -and $Task -ne 'format') { throw '-Fix is only supported with format.' }
if (!(Test-Path -LiteralPath $Go -PathType Leaf)) { throw 'Pinned Go executable not found.' }
function Invoke-Checked([string]$File, [string[]]$Arguments) {
    & $File @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Command failed ($LASTEXITCODE): $File" }
}
function Assert-Entrypoint {
    # 固定已知文件静态预检，禁止在未接入只读协议时执行主程序而误开 UI。
    $main = Get-Content -LiteralPath (Join-Path $Root 'main.go') -Raw
    $entry = Get-Content -LiteralPath (Join-Path $Root 'cmd/toolnest/run_windows.go') -Raw
    $options = Get-Content -LiteralPath (Join-Path $Root 'cmd/toolnest/options.go') -Raw
    $desktop = Get-Content -LiteralPath (Join-Path $Root 'cmd/toolnest/desktop_windows.go') -Raw
    if (!$main.Contains('toolnest.Run(') -or !$entry.Contains('packaging.WriteBuildInfo(stdout)') -or !$options.Contains('toolnest-build-info') -or !$desktop.Contains('AcquireInstallation(')) { throw 'main read-only identity/lifetime lock binding missing; refusing to execute the binary.' }
    $identityAt = $entry.IndexOf('packaging.WriteBuildInfo(stdout)')
    $startupAt = $entry.IndexOf('resolveEnvironment(flags)')
    if ($startupAt -lt 0 -or $identityAt -gt $startupAt) { throw 'Build-info must exit before normal startup.' }
}
$goIdentity = & $Go version
if ($LASTEXITCODE -ne 0 -or $goIdentity -cne "go version $PinnedGo windows/amd64") { throw 'Exact Go 1.27.4 Windows amd64 toolchain required.' }
$SavedEnvironment = @{}
foreach ($name in @('GOFLAGS','GOWORK','GOTOOLCHAIN','GOOS','GOARCH','CGO_ENABLED','GOMAXPROCS','TOOLNEST_SIGNTOOL','TOOLNEST_CERT_THUMBPRINT','TOOLNEST_TIMESTAMP_URL','TOOLNEST_ALLOW_FOREGROUND_TESTS')) { $SavedEnvironment[$name] = [Environment]::GetEnvironmentVariable($name) }
$env:GOFLAGS = '-mod=readonly'; $env:GOWORK = 'off'; $env:GOTOOLCHAIN = 'local'
$env:GOOS = 'windows'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
$env:TOOLNEST_ALLOW_FOREGROUND_TESTS = $null
# 固定工具链在高核数 Windows 主机曾触发 GC 崩溃，只限制当前构建子进程。
if (!$env:GOMAXPROCS) { $env:GOMAXPROCS = '2' }
Push-Location $Root
try {
    # 只枚举明确的源码目录，常规构建不递归扫描依赖，也不启动真窗口验收。
    $SourceDirectories = @('.', 'cmd/toolnest','internal/app','internal/core','internal/brand','internal/network','internal/update','internal/ui','internal/platform','internal/platform/lifecycle','packaging','packaging/runtime','packaging/maintenance','scripts/release','scripts/icons','tests/e2e','tests/e2e/lab','tests/e2e/runner','tests/e2e/desktop-runner','tests/e2e/manual-kit') | ForEach-Object { Join-Path $Root $_ }
    $OwnedGoFiles = @(Get-ChildItem -LiteralPath $SourceDirectories -Filter '*.go' -File | ForEach-Object FullName)
    $Gofmt = Join-Path (Split-Path $Go -Parent) 'gofmt.exe'
    if ($Task -eq 'format') {
        if ($Fix) { Invoke-Checked $Gofmt (@('-w') + $OwnedGoFiles) }
        $dirty = @(& $Gofmt -l @OwnedGoFiles)
        if ($LASTEXITCODE -ne 0 -or $dirty.Count) { throw "Formatting needed: $dirty" }
        return
    }
    $Packages = @('./...')
    if ($Task -eq 'vet') { Invoke-Checked $Go (@('vet') + $Packages); return }
    if ($Task -eq 'test') {
        Invoke-Checked $Go (@('test','-count=1') + $Packages)
        & (Join-Path $PSScriptRoot 'test-packaging.ps1')
        return
    }
    if ($Repository -notmatch '^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$') { throw 'Explicit GitHub owner/repository required.' }
    if (!$PublicKey) { throw 'Trusted public key required, independently of artifact contents.' }
    $pub = [Convert]::FromBase64String($PublicKey.Trim())
    if ($pub.Length -ne 32) { throw 'Expected 32-byte Ed25519 public key.' }
    $PublicKey = $PublicKey.Trim()
    if ($Task -eq 'verify') {
        if (!$Output) { throw 'verify requires -Output.' }
        $verifyArgs = @('run','./scripts/release','verify','--dir',$Output,'--repository',$Repository,'--public-key',$PublicKey)
        if ($Test) { $verifyArgs += '--test' }
        if ($PublicPreview) { $verifyArgs += '--public-preview' }
        Invoke-Checked $Go $verifyArgs
        return
    }
    Assert-Entrypoint
    $head = & git -C $MyGo rev-parse HEAD
    if ($LASTEXITCODE -ne 0 -or $head.Trim() -cne $PinnedMyGo) { throw 'MyGo checkout does not match the pinned commit.' }
    $dirtyFramework = @(& git -C $MyGo status --porcelain --untracked-files=no)
    if ($LASTEXITCODE -ne 0 -or $dirtyFramework.Count) { throw 'Pinned MyGo checkout has tracked modifications.' }
    $moduleText = & $Go list -m -json github.com/egoist/mygo
    if ($LASTEXITCODE) { throw 'MyGo module identity unavailable.' }
    $module = ($moduleText -join "`n") | ConvertFrom-Json
    if ($module.Version -cne 'v0.9.1-0.20261111182651-0d644f4aca24' -or $module.PSObject.Properties['Replace']) { throw 'go.mod MyGo pin mismatch or replacement present.' }
    if (!$Test) {
        # 正式构建必须存在真实匹配远端及外部签名凭据；脚本本身从不上传。
        $remote = & git remote get-url origin
        if ($LASTEXITCODE -ne 0 -or $remote.Trim() -cnotin @("https://github.com/$Repository.git","https://github.com/$Repository","git@github.com:$Repository.git")) { throw 'Production requires the real matching GitHub origin.' }
        if(!$PublicPreview){
            if (!$CertificateThumbprint -or !$SignTool) { throw 'Production requires external Authenticode credentials and SignTool.' }
            if ($CertificateThumbprint -notmatch '^[A-Fa-f0-9]{40}$') { throw 'Invalid signing certificate thumbprint.' }
            if ($TimestampUrl -notmatch '^https?://[^\s]+$') { throw 'Timestamp service URL required.' }
        }
    }
    if (!$env:MYGO_UPDATER_PRIVATE_KEY -and !$PrivateKeyFile) { throw 'External signing key required; test key generation requires a separate explicit --test command.' }
    if ($PrivateKeyFile) {
        $private = [IO.Path]::GetFullPath($PrivateKeyFile)
        if ($private.StartsWith([IO.Path]::GetFullPath($Root) + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Private key must be outside the repository.' }
        if (!$Test -and (Test-Path -LiteralPath (Join-Path (Split-Path $private -Parent) 'TEST-ONLY'))) { throw 'Test key forbidden for production.' }
    }
    if($PublicPreview-and !$PrivateKeyFile){throw 'Public previews require an explicit external signing identity.'}
    if (!$MakeNSIS) { $MakeNSIS = (Get-Command makensis.exe -ErrorAction Stop).Source }
    if ((& $MakeNSIS /VERSION) -cne 'v3.13' -or $LASTEXITCODE) { throw 'Pinned NSIS 3.13 required.' }
    if (!$Output) { $Output = Join-Path ([IO.Path]::GetTempPath()) ("toolnest-build/" + [guid]::NewGuid().ToString('N')) }
    $Output = [IO.Path]::GetFullPath($Output)
    if ($Output.StartsWith([IO.Path]::GetFullPath($Root), [StringComparison]::OrdinalIgnoreCase)) { throw 'Build output must be outside the shared repository.' }
    if (Test-Path -LiteralPath $Output) { throw 'Output must be a new directory, never overwrite a prior build.' }
    [void](New-Item -ItemType Directory -Path $Output)
    $Work = Join-Path ([IO.Path]::GetTempPath()) ('toolnest-stage-' + [guid]::NewGuid().ToString('N'))
    [void](New-Item -ItemType Directory -Path $Work)
    $BrandAssets=Join-Path $Work 'brand-assets'
    & (Join-Path $PSScriptRoot 'Build-BrandAssets.ps1') -Output $BrandAssets -Go $Go
    Copy-Item -LiteralPath (Join-Path $BrandAssets 'ToolNest.ico') -Destination $Work
    $Channel = if ($Test) { 'test' } elseif($PublicPreview) {'public-preview'} else { 'production' }
    $Flags = "-s -w -H=windowsgui -X toolnest/packaging.Repository=$Repository -X toolnest/packaging.UpdatePublicKey=$PublicKey -X toolnest/packaging.BuildChannel=$Channel"
    Invoke-Checked $Go (@('vet') + $Packages)
    Invoke-Checked $Go (@('test','-count=1') + $Packages)
    & (Join-Path $PSScriptRoot 'test-packaging.ps1')
    Invoke-Checked $Go @('build','-trimpath','-buildvcs=false','-ldflags',$Flags,'-o',(Join-Path $Work 'ToolNest.exe'),$MainPackage)
    & (Join-Path $PSScriptRoot 'Assert-WindowsGui.ps1') -Path (Join-Path $Work 'ToolNest.exe')
    Invoke-Checked $Go @('build','-trimpath','-buildvcs=false','-ldflags','-s -w','-o',(Join-Path $Work 'ToolNest-maintenance.exe'),'./packaging/maintenance')
    $infoFile = Join-Path $Work 'build-info.stdout'
    $infoError = Join-Path $Work 'build-info.stderr'
    $probe = Start-Process -FilePath (Join-Path $Work 'ToolNest.exe') -ArgumentList '--toolnest-build-info' -NoNewWindow -PassThru -RedirectStandardOutput $infoFile -RedirectStandardError $infoError
    # 即使静态检查通过，仍限制等待时间；不强杀，超时要求关闭本次探针后重试。
    if (!$probe.WaitForExit(10000)) { throw 'Build-info probe did not exit; close it normally. No process was killed.' }
    if ($probe.ExitCode -ne 0) { throw 'Read-only build-info probe failed.' }
    $identityText = Get-Content -LiteralPath $infoFile -Raw
    $identity = $identityText | ConvertFrom-Json
    if ($identity.version -cne $Version -or $identity.repository -cne $Repository -or $identity.publicKey -cne $PublicKey -or $identity.channel -cne $Channel -or $identity.runtimeProtocol -ne 1) { throw 'Executable build identity mismatch or lifecycle API unbound.' }
    $identityText | Set-Content -LiteralPath (Join-Path $Work 'release-identity.json') -Encoding utf8NoBOM
    Copy-Item -LiteralPath (Join-Path $Work 'release-identity.json') -Destination $Output
    function Sign-Production([string]$File) {
        if ($Test-or $PublicPreview) { return }
        Invoke-Checked $SignTool @('sign','/sha1',$CertificateThumbprint,'/fd','SHA256','/tr',$TimestampUrl,'/td','SHA256',$File)
        Invoke-Checked $SignTool @('verify','/pa','/all',$File)
        $auth = Get-AuthenticodeSignature -LiteralPath $File
        if ($auth.Status -ne 'Valid' -or $auth.SignerCertificate.Thumbprint -cne $CertificateThumbprint.ToUpperInvariant()) { throw "Authenticode verification failed: $File" }
    }
    Sign-Production (Join-Path $Work 'ToolNest.exe')
    Sign-Production (Join-Path $Work 'ToolNest-maintenance.exe')
    $installer = Join-Path $Output "ToolNest-Setup-$Version-windows-amd64.exe"
    $nsisArgs = @('/V3','/WX','/INPUTCHARSET','UTF8',"/DVERSION=$Version","/DPAYLOAD=$Work","/DOUTPUT=$installer")
    if (!$Test-and !$PublicPreview) {
        $env:TOOLNEST_SIGNTOOL = $SignTool
        $env:TOOLNEST_CERT_THUMBPRINT = $CertificateThumbprint
        $env:TOOLNEST_TIMESTAMP_URL = $TimestampUrl
        $nsisArgs += "/DSIGN_UNINSTALLER=$(Join-Path $PSScriptRoot 'sign-uninstaller.ps1')"
    }
    $nsisArgs += (Join-Path $Root 'packaging/toolnest.nsi')
    Invoke-Checked $MakeNSIS $nsisArgs
    Sign-Production $installer
    $bundleArgs = @('run','./scripts/release','bundle','--dir',$Output,'--exe',(Join-Path $Work 'ToolNest.exe'),'--root',$Root,'--repository',$Repository,'--public-key',$PublicKey)
    if ($PrivateKeyFile) { $bundleArgs += @('--key-file',$PrivateKeyFile) }
    if ($Test) { $bundleArgs += '--test' }
    if ($PublicPreview) { $bundleArgs += '--public-preview' }
    Invoke-Checked $Go $bundleArgs
    Write-Output "Verified $Channel artifacts: $Output"
} finally {
    Pop-Location
    foreach ($name in $SavedEnvironment.Keys) { [Environment]::SetEnvironmentVariable($name, $SavedEnvironment[$name]) }
}
