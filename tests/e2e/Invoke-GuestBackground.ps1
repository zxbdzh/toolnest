#requires -Version 7.2
[CmdletBinding()]
param(
 [ValidateSet('Probe','Acceptance')][string]$Mode='Probe',
 [string]$Go='go.exe',
 [Parameter(Mandatory)][string]$Versions,
 [ValidateRange(30,900)][int]$TimeoutSeconds=120
)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$repository=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$root=Join-Path ([IO.Path]::GetTempPath()) ('toolnest-guest-background-'+(Get-Date -Format yyyyMMdd-HHmmss)+'-'+[guid]::NewGuid().ToString('N').Substring(0,8))
$inputRoot=Join-Path $root 'input';$evidence=Join-Path $root 'evidence'
[void][IO.Directory]::CreateDirectory($inputRoot)
$utf8=[Text.UTF8Encoding]::new($true)
function Boundary {
 $values=@()
 foreach($entry in @(@('HKCU:\Environment','Path'),@('HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment','Path'),@('HKCU:\Software\Microsoft\Windows\CurrentVersion\Run','ToolNest'),@('HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ToolNest','InstallLocation'))){
  $key=Get-Item -LiteralPath $entry[0] -ErrorAction SilentlyContinue
  if(!$key-or $entry[1]-notin $key.GetValueNames()){$values+='missing';continue}
  $raw=$key.GetValueKind($entry[1]).ToString()+':'+$key.GetValue($entry[1],$null,[Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
  $values+=[Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::Unicode.GetBytes($raw)))
 }
 $application=Join-Path $env:LOCALAPPDATA 'Programs\ToolNest\ToolNest.exe'
 $values+=if(Test-Path -LiteralPath $application){(Get-FileHash -LiteralPath $application).Hash}else{'missing'}
 return $values
}
$before=Boundary
$saved=@{};foreach($name in @('GOMAXPROCS','GOTOOLCHAIN','GOWORK','GOFLAGS')){$saved[$name]=[Environment]::GetEnvironmentVariable($name)}
try {
 $env:GOMAXPROCS='2';$env:GOTOOLCHAIN='local';$env:GOWORK='off';$env:GOFLAGS='-mod=readonly'
 Push-Location $repository
 try{
  $runner=Join-Path $inputRoot 'desktop-runner.exe'
  & $Go build -trimpath -buildvcs=false -o $runner ./tests/e2e/desktop-runner
  if($LASTEXITCODE){throw 'Private desktop runner compilation failed'}
  foreach($name in @('Guest-Acceptance.ps1','Window-Automation.ps1')){[IO.File]::WriteAllText((Join-Path $inputRoot $name),[IO.File]::ReadAllText((Join-Path $PSScriptRoot $name)),$utf8)}
  if($Mode-eq 'Acceptance'){
   & $Go build -trimpath -buildvcs=false -o (Join-Path $inputRoot 'acceptance-runner.exe') ./tests/e2e/runner
   if($LASTEXITCODE){throw 'Guest fixture compilation failed'}
   & $Go test -c -mod=readonly -o (Join-Path $inputRoot 'acceptance.test.exe') ./tests/e2e
   if($LASTEXITCODE){throw 'Guest integration compilation failed'}
   foreach($version in @('0.1.0','0.1.1')){Copy-Item -LiteralPath (Join-Path $Versions ($version+'\ToolNest-Setup-'+$version+'.exe')) -Destination $inputRoot}
   Copy-Item -LiteralPath (Join-Path $Versions '0.1.1\ToolNest.exe') -Destination $inputRoot
   foreach($name in @('update-0.1.1.tar.gz','update-0.1.1.sig','update.pub')){Copy-Item -LiteralPath (Join-Path $Versions ('signed-update\'+$name)) -Destination $inputRoot}
  }
  $escape={param($value)[Security.SecurityElement]::Escape($value)}
  $guestMode=if($Mode-eq 'Probe'){' -Probe'}else{''}
  $wsb=Join-Path $root 'ToolNest.wsb'
  # 只读输入、专用可写证据与关闭网络/剪贴板；绝不映射宿主用户目录或真实程序目录。
  $xml='<Configuration><VGpu>Disable</VGpu><Networking>Disable</Networking><ClipboardRedirection>Disable</ClipboardRedirection><AudioInput>Disable</AudioInput><VideoInput>Disable</VideoInput><PrinterRedirection>Disable</PrinterRedirection><MappedFolders><MappedFolder><HostFolder>'+(& $escape $inputRoot)+'</HostFolder><SandboxFolder>C:\ToolNestAcceptance\input</SandboxFolder><ReadOnly>true</ReadOnly></MappedFolder><MappedFolder><HostFolder>'+(& $escape $evidence)+'</HostFolder><SandboxFolder>C:\ToolNestAcceptance\evidence</SandboxFolder><ReadOnly>false</ReadOnly></MappedFolder></MappedFolders><LogonCommand><Command>powershell.exe -NoProfile -ExecutionPolicy Bypass -File C:\ToolNestAcceptance\input\Guest-Acceptance.ps1'+$guestMode+'</Command></LogonCommand></Configuration>'
  [IO.File]::WriteAllText($wsb,$xml,[Text.UTF8Encoding]::new($false))
  & $runner --application (Join-Path $Versions '0.1.0\ToolNest.exe') --worker (Join-Path $PSScriptRoot 'Sandbox-Background-Worker.ps1') --out $evidence --scenario sandbox --sandbox-config $wsb --timeout ($TimeoutSeconds.ToString()+'s')
  $runnerExit=$LASTEXITCODE
  $workerFile=Join-Path $evidence 'worker.json'
  $worker=if(Test-Path -LiteralPath $workerFile){Get-Content -LiteralPath $workerFile -Raw|ConvertFrom-Json}else{$null}
  $unchanged=($before-join '|')-ceq ((Boundary)-join '|')
  $isolationFile=Join-Path $evidence 'isolation.json'
  $isolation=if(Test-Path -LiteralPath $isolationFile){Get-Content -LiteralPath $isolationFile -Raw|ConvertFrom-Json}else{$null}
  $isolated=$isolation-and $isolation.inputDesktopUnchanged-and !$isolation.workerOnForeground-and $isolation.childrenRemaining-eq 0
  $timedOut=$isolation-and ($isolation.PSObject.Properties.Name-contains 'error')-and $isolation.error-ceq 'background desktop worker timed out'
  $status=if(!$unchanged-or !$isolated){'failed'}elseif($timedOut){'blocked'}elseif($runnerExit-ne 0){'failed'}elseif($worker){$worker.status}else{'blocked'}
  $summary=@{status=$status;mode=$Mode;hostBoundaryUnchanged=$unchanged;desktopIsolationVerified=[bool]$isolated;guestStarted=[bool]($worker-and $worker.guestStarted);evidence=$evidence;complete=$false}
  [IO.File]::WriteAllText((Join-Path $root 'result.json'),($summary|ConvertTo-Json -Depth 6),[Text.UTF8Encoding]::new($false))
  $summary|ConvertTo-Json -Depth 6
  if($status-eq 'failed'){throw 'Background guest acceptance failed; inspect isolated evidence'}
 }finally{Pop-Location}
}finally{foreach($name in $saved.Keys){[Environment]::SetEnvironmentVariable($name,$saved[$name])}}
if($status-eq 'blocked'){exit 2}
