#requires -Version 7.2
[CmdletBinding()]
param()
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
if ($env:USERNAME -eq 'WDAGUtilityAccount') { throw 'Negative host safety checks must run outside Sandbox.' }
foreach($file in @(Get-ChildItem -LiteralPath $PSScriptRoot -Filter '*.ps1' -File)){
    $tokens=$null;$parseErrors=$null
    [void][Management.Automation.Language.Parser]::ParseFile($file.FullName,[ref]$tokens,[ref]$parseErrors)
    if($parseErrors.Count){throw ($file.Name+': '+(($parseErrors|ForEach-Object Message)-join '; '))}
}
$updateRejected=$false
try{& (Join-Path $PSScriptRoot 'Invoke-Background.ps1') -Scenario update-ui -EvidenceRoot (Join-Path ([IO.Path]::GetTempPath()) 'toolnest-background-host-denied')}catch{
    if($_.Exception.Message -notlike '*UI update evidence must stay inside*'){throw};$updateRejected=$true
}
if(!$updateRejected){throw 'Update acceptance allowed a fixture path outside its reserved root.'}
$keyRotationRejected=$false
try{& (Join-Path $PSScriptRoot 'Build-TestVersions.ps1') -MakeNSIS 'not-used-before-key-validation'}catch{
    if($_.Exception.Message -notlike '*Published test source requires -SigningKeyDirectory*'){throw};$keyRotationRejected=$true
}
if(!$keyRotationRejected){throw 'Published test source silently generated a different signing key.'}
$forgedConfig=Join-Path ([IO.Path]::GetTempPath()) ('toolnest-desktop-negative-'+[guid]::NewGuid().ToString('N')+'.json')
# 此夹具只有不存在的测试桌面名；不提供输出目录，确保失败先于结果写入和进程启动。
[IO.File]::WriteAllText($forgedConfig,'{"Desktop":"ToolNest.Test.0.0","Scenario":"sandbox"}',[Text.UTF8Encoding]::new($false))
$sandboxRejected=$false
try{& (Join-Path $PSScriptRoot 'Sandbox-Background-Worker.ps1') -Config $forgedConfig}catch{
    if($_.Exception.Message -notlike '*Sandbox worker is not on its independently created desktop*'){throw};$sandboxRejected=$true
}
if(!$sandboxRejected){throw 'Sandbox worker accepted a forged desktop configuration on the host.'}
foreach($probe in @($true,$false)){
    $guestRejected=$false
    try{& (Join-Path $PSScriptRoot 'Guest-Acceptance.ps1') -Probe:$probe}catch{
        if($_.Exception.Message -notlike '*restricted to the mapped Windows Sandbox*'){throw};$guestRejected=$true
    }
    if(!$guestRejected){throw 'Guest installer script accepted host execution.'}
}
foreach($action in @('Capture','Inspect','Keyboard','Invoke','SetValue','Select','Toggle','Scroll','Resize','Close')){
  foreach($noCapture in @($false,$true)){
    $rejected=$false
    # 无效 PID 也应先被环境门禁拒绝；绝不进入宿主进程、桌面或窗口枚举。
    try{& (Join-Path $PSScriptRoot 'Window-Automation.ps1') -ProcessId 0 -Action $action -ControlName 'host-denied' -Value 'Off' -NoCapture:$noCapture}catch{
        if($_.Exception.Message -notlike '*restricted to Windows Sandbox*'){throw};$rejected=$true
    }
    if(!$rejected){throw "Host UI action accepted: $action"}
  }
}
Write-Output 'PASS: PowerShell syntax; published signing key continuity, forged Sandbox desktop, invalid update fixture root, guest Probe/full and all ten UI actions with/without NoCapture (25 refusals) reject unsafe execution before process/UI access. No GUI, Sandbox, installer or application was started.'
