#requires -Version 7.2
[CmdletBinding()]
param(
    [string]$Go = 'go.exe',
    [Parameter(Mandatory)][string]$MakeNSIS,
    [Parameter(Mandatory)][string]$SigningKeyDirectory,
    [ValidatePattern('^0\.1\.[0-9]+$')][string]$Baseline='0.1.5',
    [ValidatePattern('^0\.1\.[0-9]+$')][string]$Target='0.1.6'
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
if([version]$Target-le [version]$Baseline){throw 'Target must be newer than baseline.'}
$repository = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$policy = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'TestUpdateSource.json') -Raw | ConvertFrom-Json
$SigningKeyDirectory = [IO.Path]::GetFullPath($SigningKeyDirectory)
if ($SigningKeyDirectory.StartsWith($repository + '\', [StringComparison]::OrdinalIgnoreCase)) { throw 'Signing keys must remain external.' }
foreach ($filename in @('TEST-ONLY', 'update.key', 'update.pub')) {
    if (!(Test-Path -LiteralPath (Join-Path $SigningKeyDirectory $filename) -PathType Leaf)) { throw 'Existing external TEST-ONLY signing identity required.' }
}
$publicKey = (Get-Content -LiteralPath (Join-Path $SigningKeyDirectory 'update.pub') -Raw).Trim()
if ($publicKey -cne $policy.publicKey) { throw 'Do not silently rotate the existing test identity.' }
if ((& $Go version) -cne 'go version go1.27.4 windows/amd64') { throw 'Pinned Go toolchain required.' }
if ((& $MakeNSIS /VERSION) -cne 'v3.13') { throw 'Pinned NSIS 3.13 required.' }
$kitID = [guid]::NewGuid().ToString('N')
$root = Join-Path ([IO.Path]::GetTempPath()) ('toolnest-manual-update-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + $kitID.Substring(0,8))
if (Test-Path -LiteralPath $root) { throw 'Output must be new.' }
[void][IO.Directory]::CreateDirectory($root)
$deliver = Join-Path $root '交付包'
[void][IO.Directory]::CreateDirectory($deliver)
$utf8 = [Text.UTF8Encoding]::new($false)
$saved = @{}
foreach ($name in @('GOTOOLCHAIN','GOWORK','GOFLAGS','GOMAXPROCS','CGO_ENABLED')) { $saved[$name] = [Environment]::GetEnvironmentVariable($name) }
function Invoke-Checked([string]$File, [string[]]$Arguments) {
    & $File @Arguments
    if ($LASTEXITCODE -ne 0) { throw "Command failed ($LASTEXITCODE): $File" }
}
try {
    $env:GOTOOLCHAIN = 'local'; $env:GOWORK = 'off'; $env:GOFLAGS = '-mod=readonly'; $env:GOMAXPROCS = '2'; $env:CGO_ENABLED = '0'
    Push-Location $repository
    try {
        $versionFile = Join-Path $repository 'packaging\version.go'
        $original = Get-Content -LiteralPath $versionFile -Raw
        $anchor = 'return strings.TrimSpace(version)'
        if ($original.Split($anchor).Count -ne 2) { throw 'Version overlay contract changed.' }
        $applications = @{}
        $brandAssets=Join-Path $root 'brand-assets'
        & (Join-Path $repository 'scripts\Build-BrandAssets.ps1') -Output $brandAssets -Go $Go
        foreach ($version in @($Baseline,$Target)) {
            $stage = Join-Path $root $version
            [void][IO.Directory]::CreateDirectory($stage)
            $replacement = Join-Path $stage 'version-overlay.go'
            [IO.File]::WriteAllText($replacement, $original.Replace($anchor, ('return strings.TrimSpace("' + $version + '")')), $utf8)
            $mapping = @{}; $mapping[$versionFile] = $replacement
            $overlay = Join-Path $stage 'overlay.json'
            [IO.File]::WriteAllText($overlay, (@{Replace=$mapping} | ConvertTo-Json -Depth 4), $utf8)
            $application = Join-Path $stage 'ToolNest.exe'
            $linkFlags = "-s -w -H=windowsgui -X toolnest/packaging.Repository=$($policy.repository) -X toolnest/packaging.UpdatePublicKey=$publicKey -X toolnest/packaging.BuildChannel=manual-test"
            Invoke-Checked $Go @('build','-trimpath','-buildvcs=false',('-overlay=' + $overlay),'-ldflags',$linkFlags,'-o',$application,'.')
            & (Join-Path $repository 'scripts\Assert-WindowsGui.ps1') -Path $application
            $identity = & (Join-Path $repository 'scripts\Read-BuildInfo.ps1') -Path $application | ConvertFrom-Json
            if ($identity.version -cne $version -or $identity.channel -cne 'manual-test' -or $identity.publicKey -cne $publicKey) { throw 'Application identity mismatch.' }
            $applications[$version] = $application
        }
        $runner = Join-Path $root 'acceptance-runner.exe'
        Invoke-Checked $Go @('build','-trimpath','-buildvcs=false','-o',$runner,'./tests/e2e/runner')
        $signed = Join-Path $root 'signed-update'
        Invoke-Checked $runner @('--operation','sign','--out',$signed,'--version',$Target,'--exe',$applications[$Target],'--key-file',(Join-Path $SigningKeyDirectory 'update.key'))
        $launcher = Join-Path $root 'ToolNest-TestLauncher.exe'
        Invoke-Checked $Go @('build','-trimpath','-buildvcs=false','-ldflags',("-s -w -H=windowsgui -X main.kitID=$kitID -X main.trustedPublicKey=$publicKey"),'-o',$launcher,'./tests/e2e/manual-kit')
        & (Join-Path $repository 'scripts\Assert-WindowsGui.ps1') -Path $launcher
        $notes = "# ToolNest $Target（本地更新流程测试）`n`n" + (Get-Content -LiteralPath (Join-Path $repository ("docs\releases\$Baseline.md")) -Raw).Replace("# ToolNest $Baseline", '').Trim() + "`n`n此版本与起始版使用同一批修复代码，仅版本号不同，供验证更新体验。"
        $config = @{
            ID=$kitID; Baseline=$Baseline; Target=$Target; PublicKey=$publicKey; Notes=$notes
            BaselineSHA256=(Get-FileHash -LiteralPath $applications[$Baseline] -Algorithm SHA256).Hash.ToLowerInvariant()
            TargetSHA256=(Get-FileHash -LiteralPath $applications[$Target] -Algorithm SHA256).Hash.ToLowerInvariant()
        }
        $instructions = (Get-Content -LiteralPath (Join-Path $repository 'tests\e2e\manual-kit\操作步骤.txt') -Raw).
            Replace('{{KIT_ID}}', $kitID).Replace('{{KIT_SHORT}}', $kitID.Substring(0,8)).Replace('0.1.3',$Baseline).Replace('0.1.4',$Target)
        [IO.File]::WriteAllText((Join-Path $deliver '先读我-完整操作步骤.txt'), $instructions, $utf8)
        $installers = @()
        foreach ($version in @($Baseline,$Target)) {
            $payload = Join-Path $root ('payload-' + $version)
            [void][IO.Directory]::CreateDirectory($payload)
            Copy-Item -LiteralPath $applications[$version] -Destination (Join-Path $payload 'ToolNest.exe')
            Copy-Item -LiteralPath $launcher -Destination $payload
            Copy-Item -LiteralPath (Join-Path $brandAssets 'ToolNest.ico') -Destination $payload
            Copy-Item -LiteralPath (Join-Path $signed ("update-$Target.tar.gz")) -Destination (Join-Path $payload 'update.tar.gz')
            Copy-Item -LiteralPath (Join-Path $signed ("update-$Target.sig")) -Destination (Join-Path $payload 'update.sig')
            Copy-Item -LiteralPath (Join-Path $signed 'update.pub') -Destination $payload
            [IO.File]::WriteAllText((Join-Path $payload 'kit.json'), ($config | ConvertTo-Json -Depth 4), $utf8)
            [IO.File]::WriteAllText((Join-Path $payload '操作步骤.txt'), $instructions, $utf8)
            # 此检查只读资产；没有 --open，不创建测试配置或注册表，更不执行安装器。
            $start = [Diagnostics.ProcessStartInfo]::new((Join-Path $payload 'ToolNest-TestLauncher.exe'), '--verify-assets')
            $start.UseShellExecute=$false; $start.CreateNoWindow=$true
            $start.RedirectStandardOutput=$true; $start.RedirectStandardError=$true
            $process = [Diagnostics.Process]::Start($start)
            $stdout=$process.StandardOutput.ReadToEndAsync(); $stderr=$process.StandardError.ReadToEndAsync()
            if (!$process.WaitForExit(10000)) { throw 'Read-only kit verification timed out.' }
            $verified=$stdout.GetAwaiter().GetResult()
            if ($process.ExitCode -ne 0) { throw ('Kit verification failed: ' + $stderr.GetAwaiter().GetResult()) }
            [IO.File]::WriteAllText((Join-Path $root ('verified-' + $version + '.json')), $verified, $utf8)
            $process.Dispose()
            $purpose = if ($version -eq $Baseline) { '起始版' } else { '目标版-备用直装' }
            $installer = Join-Path $deliver ("ToolNest-UpdateTest-$version-$purpose.exe")
            Invoke-Checked $MakeNSIS @('/V3','/INPUTCHARSET','UTF8',("/DVERSION=$version"),("/DKIT_ID=$kitID"),("/DKIT_SHORT=" + $kitID.Substring(0,8)),("/DPAYLOAD=$payload"),("/DOUTPUT=$installer"),(Join-Path $repository 'tests\e2e\manual-kit\installer.nsi'))
            $installers += $installer
        }
        $sums = @($installers | ForEach-Object { (Get-FileHash -LiteralPath $_ -Algorithm SHA256).Hash.ToLowerInvariant() + '  ' + [IO.Path]::GetFileName($_) })
        [IO.File]::WriteAllText((Join-Path $deliver 'SHA256SUMS.txt'), ($sums -join "`n") + "`n", $utf8)
        $result = @{
            status='prepared'; kitID=$kitID; baseline=$Baseline; target=$Target; directory=$deliver
            baselineInstaller=$installers[0]; targetInstaller=$installers[1]; sourcePublished=$false
            root=$root; launcher=$launcher; signedUpdate=$signed; applicationBaseline=$applications[$Baseline]; applicationTarget=$applications[$Target]
            sourceCodeVersion=(Get-Content -LiteralPath (Join-Path $repository 'packaging\VERSION') -Raw).Trim()
            authentiCode='NotSigned'; privateKeyIncluded=$false; installedAutomatically=$false
        }
        [IO.File]::WriteAllText((Join-Path $root 'kit-result.json'), ($result | ConvertTo-Json -Depth 4), $utf8)
        $result | ConvertTo-Json -Depth 4
    } finally { Pop-Location }
} finally {
    foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name, $saved[$name]) }
}
