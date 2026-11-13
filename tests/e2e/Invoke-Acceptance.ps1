#requires -Version 7.2
[CmdletBinding()]
param(
    [ValidateSet('Integration','Build','Sandbox','Probe')][string]$Mode = 'Integration',
 [string]$Go = 'go.exe',
 [string]$OutputRoot = (Join-Path ([IO.Path]::GetTempPath()) 'toolnest-acceptance'),
    [string]$Installer010,
    [string]$Installer011,
    [string]$Application,
    [string]$SignedUpdate,
    [int]$TimeoutSeconds = 180
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$Repository = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$OutputRoot = [IO.Path]::GetFullPath($OutputRoot)
if ($TimeoutSeconds -lt 15 -or $TimeoutSeconds -gt 900) { throw 'TimeoutSeconds must be 15..900.' }
$Run = Join-Path $OutputRoot ((Get-Date -Format 'yyyyMMdd-HHmmss') + '-' + [guid]::NewGuid().ToString('N').Substring(0,8))
$InputRoot = Join-Path $Run 'input'
$Evidence = Join-Path $Run 'evidence'
[void][IO.Directory]::CreateDirectory($InputRoot)
[void][IO.Directory]::CreateDirectory($Evidence)
$Results = [Collections.Generic.List[object]]::new()
$Utf8 = [Text.UTF8Encoding]::new($false)
function Save-Json([string]$Path, $Value) { [IO.File]::WriteAllText($Path, ($Value | ConvertTo-Json -Depth 20), $Utf8) }
function Record([string]$Name,[string]$Status,[string]$Detail) { $Results.Add([ordered]@{name=$Name; status=$Status; detail=$Detail}) }
function Get-HostBoundary {
    # 只读取宿主边界的摘要，既不导出真实 PATH，也不枚举其他用户数据。
    $entries = @(
        @('HKCU:\Environment','Path'),
        @('HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment','Path'),
        @('HKCU:\Software\Microsoft\Windows\CurrentVersion\Run','ToolNest'),
        @('HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ToolNest','InstallLocation')
    )
    $hashes = @()
    foreach ($entry in $entries) {
        $key = Get-Item -LiteralPath $entry[0] -ErrorAction SilentlyContinue
        if ($null -eq $key -or $entry[1] -notin $key.GetValueNames()) { $hashes += 'missing'; continue }
        $kind = $key.GetValueKind($entry[1]).ToString()
        $value = $key.GetValue($entry[1],$null,[Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        $encoded = [Text.Encoding]::Unicode.GetBytes(($kind + ':' + [string]$value))
        $hashes += [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData($encoded))
    }
    $program = Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'Programs\ToolNest\ToolNest.exe'
    $hashes += if (Test-Path -LiteralPath $program -PathType Leaf) { (Get-FileHash -LiteralPath $program -Algorithm SHA256).Hash } else { 'missing' }
    return $hashes
}
function Invoke-Logged([string]$File,[string[]]$Arguments,[string]$Log) {
    & $File @Arguments 2>&1 | Tee-Object -FilePath $Log | Out-Host
    if ($LASTEXITCODE -ne 0) { throw "Command failed ($LASTEXITCODE): $File; evidence=$Log" }
}
$Before = Get-HostBoundary
$Saved = @{}
foreach ($name in @('GOTOOLCHAIN','GOFLAGS','GOWORK','TEMP','TMP','TOOLNEST_ACCEPTANCE_SIGNED_UPDATE')) { $Saved[$name] = [Environment]::GetEnvironmentVariable($name) }
try {
    $env:GOTOOLCHAIN='local'; $env:GOFLAGS='-mod=readonly'; $env:GOWORK='off'
    $env:TOOLNEST_ACCEPTANCE_SIGNED_UPDATE=if($SignedUpdate){[IO.Path]::GetFullPath($SignedUpdate)}else{$null}
    $env:TEMP=Join-Path $Run 'temp'; $env:TMP=$env:TEMP
    [void][IO.Directory]::CreateDirectory($env:TEMP)
    Save-Json (Join-Path $Evidence 'environment.json') ([ordered]@{os=[Environment]::OSVersion.VersionString; powershell=$PSVersionTable.PSVersion.ToString(); date=(Get-Date).ToString('o'); mode=$Mode; scope='harness + isolated integration, not full product acceptance'})
    Push-Location $Repository
    try {
        if ($Mode -ne 'Probe') {
            & (Join-Path $PSScriptRoot 'Test-Safety.ps1')
            Record 'script-safety' 'pass' 'Parsed all PowerShell files; guest installer and all desktop helpers reject host execution before accessing processes or UI.'
            Invoke-Logged $Go @('version') (Join-Path $Evidence 'go-version.txt')
            Invoke-Logged $Go @('test','-count=1','-timeout=90s','-json','./tests/e2e/...') (Join-Path $Evidence 'integration.jsonl')
            if($SignedUpdate){Record 'real-application-signed-staging' 'pass' 'Built application 0.1.1 archive downloaded and verified through both authenticated proxies; installed lifecycle not implied.'}
            Record 'isolated-integration' 'pass' 'Executed real core/app/platform/network/update public boundaries; synthetic executable payloads are not installed versions.'
            Invoke-Logged $Go @('build','-trimpath','-buildvcs=false','-o',(Join-Path $InputRoot 'acceptance-runner.exe'),'./tests/e2e/runner') (Join-Path $Evidence 'build-runner.txt')
            Invoke-Logged $Go @('test','-c','-o',(Join-Path $InputRoot 'acceptance.test.exe'),'./tests/e2e') (Join-Path $Evidence 'build-tests.txt')
            # Sandbox 内置 Windows PowerShell 5.1 按 ANSI 猜测无 BOM 文件，必须在客体副本保留 UTF-8 BOM。
            foreach ($name in @('Guest-Acceptance.ps1','Window-Automation.ps1')) { [IO.File]::WriteAllText((Join-Path $InputRoot $name),(Get-Content -LiteralPath (Join-Path $PSScriptRoot $name) -Raw -Encoding utf8),[Text.UTF8Encoding]::new($true)) }
            foreach ($pair in @(@($Installer010,'ToolNest-Setup-0.1.0.exe'),@($Installer011,'ToolNest-Setup-0.1.1.exe'),@($Application,'ToolNest.exe'))) {
                if ($pair[0]) { Copy-Item -LiteralPath ([IO.Path]::GetFullPath($pair[0])) -Destination (Join-Path $InputRoot $pair[1]) }
            }
            if($SignedUpdate){foreach($name in @('update-0.1.1.tar.gz','update-0.1.1.sig','update.pub')){Copy-Item -LiteralPath (Join-Path $SignedUpdate $name) -Destination (Join-Path $InputRoot $name)}}
        }
    } finally { Pop-Location }
    if ($Mode -in @('Sandbox','Probe')) {
        if ($Mode -eq 'Probe') { [IO.File]::WriteAllText((Join-Path $InputRoot 'Guest-Acceptance.ps1'),(Get-Content -LiteralPath (Join-Path $PSScriptRoot 'Guest-Acceptance.ps1') -Raw -Encoding utf8),[Text.UTF8Encoding]::new($true)) }
        $Escape = { param($Text) [Security.SecurityElement]::Escape($Text) }
        $escapedInput = & $Escape $InputRoot
        $escapedOutput = & $Escape $Evidence
        $guestMode = if ($Mode -eq 'Probe') { ' -Probe' } else { '' }
        $configuration = @"
<Configuration>
  <VGpu>Disable</VGpu><Networking>Disable</Networking><ClipboardRedirection>Disable</ClipboardRedirection>
  <AudioInput>Disable</AudioInput><VideoInput>Disable</VideoInput><PrinterRedirection>Disable</PrinterRedirection>
  <MappedFolders>
    <MappedFolder><HostFolder>$escapedInput</HostFolder><SandboxFolder>C:\ToolNestAcceptance\input</SandboxFolder><ReadOnly>true</ReadOnly></MappedFolder>
    <MappedFolder><HostFolder>$escapedOutput</HostFolder><SandboxFolder>C:\ToolNestAcceptance\evidence</SandboxFolder><ReadOnly>false</ReadOnly></MappedFolder>
  </MappedFolders>
  <LogonCommand><Command>powershell.exe -NoProfile -ExecutionPolicy Bypass -File C:\ToolNestAcceptance\input\Guest-Acceptance.ps1$guestMode</Command></LogonCommand>
</Configuration>
"@
        $wsb = Join-Path $Run 'ToolNest.wsb'
        [IO.File]::WriteAllText($wsb,$configuration,$Utf8)
        $sandbox = Join-Path $env:WINDIR 'System32\WindowsSandbox.exe'
        if (!(Test-Path -LiteralPath $sandbox -PathType Leaf)) { Record 'sandbox' 'blocked' 'WindowsSandbox.exe missing.' }
        else {
            # 只有明确授权的 Sandbox GUI 可见启动；没有在宿主启动任何常驻服务。
            $process = Start-Process -FilePath $sandbox -ArgumentList ('"' + $wsb + '"') -PassThru
            Save-Json (Join-Path $Evidence 'sandbox-launch.json') @{pid=$process.Id; executableVersion=(Get-Item -LiteralPath $sandbox).VersionInfo.FileVersion; wsb=$wsb}
            $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
            $completion = Join-Path $Evidence 'guest-result.json'
            while ([DateTime]::UtcNow -lt $deadline -and !(Test-Path -LiteralPath $completion)) {
                $process.Refresh()
                if ($process.HasExited -and $process.ExitCode -ne 0) { break }
                Start-Sleep -Milliseconds 500
            }
            $process.Refresh()
            if (Test-Path -LiteralPath $completion) {
                $guestResult = Get-Content -LiteralPath $completion -Raw | ConvertFrom-Json
                Record 'sandbox' $guestResult.status ('Guest evidence: ' + $completion)
            } else {
                $exitCode = if ($process.HasExited) { $process.ExitCode } else { $null }
                $exitHex=if($null -ne $exitCode){'0x'+[Convert]::ToString([int]$exitCode,16).PadLeft(8,'0')}else{$null}
                Save-Json (Join-Path $Evidence 'sandbox-failure.json') @{status='blocked'; exitCode=$exitCode; exitHex=$exitHex; guestMarkerExists=$false; timeoutSeconds=$TimeoutSeconds; processStillRunning=(!$process.HasExited)}
                Record 'sandbox' 'blocked' "No guest completion marker; launcher exit=$exitCode. Do not enable Windows features or reboot automatically."
            }
        }
    }
    Record 'production-release' 'blocked' 'Production remote/public key and Authenticode credentials require independent release evidence; test signing is insufficient.'
} catch {
    Record 'execution' 'fail' $_.Exception.Message
    [IO.File]::WriteAllText((Join-Path $Evidence 'exception.txt'), $_.ToString(), $Utf8)
} finally {
    foreach ($name in $Saved.Keys) { [Environment]::SetEnvironmentVariable($name,$Saved[$name]) }
    $After = Get-HostBoundary
    if (($Before -join '|') -cne ($After -join '|')) { Record 'host-boundary' 'fail' 'HOST SAFETY VIOLATION: production PATH/Run/install boundary changed.' }
    else { Record 'host-boundary' 'pass' 'Production PATH/Run/uninstall registration and installed executable hashes unchanged.' }
    $overall = if (@($Results | Where-Object status -eq 'fail').Count) { 'fail' } elseif (@($Results | Where-Object status -eq 'blocked').Count) { 'blocked' } else { 'pass' }
    Save-Json (Join-Path $Evidence 'result.json') @{status=$overall; complete=$false; results=@($Results); evidence=$Evidence}
    Write-Output "Evidence: $Evidence"
    Write-Output "Status: $overall (full acceptance is never implied by harness pass)"
}
if ($overall -eq 'fail') { exit 1 }
if ($overall -eq 'blocked') { exit 2 }
