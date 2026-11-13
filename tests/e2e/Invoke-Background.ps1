#requires -Version 7.2
[CmdletBinding()]
param(
 [ValidateSet('smoke','inspect','entry','update-ui')][string]$Scenario='inspect',
 [string]$Go='go.exe',
 [string]$EvidenceRoot=(Join-Path ([IO.Path]::GetTempPath()) 'toolnest-background'),
 [ValidateRange(10,180)][int]$TimeoutSeconds=90,
 [string]$PreviousApplication,
 [string]$UpdatedApplication,
 [string]$SignedUpdate
)
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
$repository=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$evidence=[IO.Path]::GetFullPath($EvidenceRoot)
if($evidence.StartsWith($repository+'\',[StringComparison]::OrdinalIgnoreCase)-or $evidence-eq $repository){throw 'Private acceptance files must stay outside the repository'}
if($Scenario-eq 'update-ui'){
 $reserved=Join-Path ([IO.Path]::GetTempPath()) 'toolnest-acceptance'
 if(!$evidence.StartsWith($reserved+'\',[StringComparison]::OrdinalIgnoreCase)){throw 'UI update evidence must stay inside the system temporary acceptance root'}
}
[void][IO.Directory]::CreateDirectory($evidence)
$run=Join-Path $evidence ((Get-Date -Format 'yyyyMMdd-HHmmss')+'-'+[guid]::NewGuid().ToString('N').Substring(0,8))
[void][IO.Directory]::CreateDirectory($run)
$programs=Join-Path $run 'programs';[void][IO.Directory]::CreateDirectory($programs)
$worker=Join-Path $PSScriptRoot 'Background-Worker.ps1'
$saved=@{};foreach($name in @('GOMAXPROCS','GOFLAGS','GOTOOLCHAIN','GOWORK','TOOLNEST_ALLOW_FOREGROUND_TESTS')){$saved[$name]=[Environment]::GetEnvironmentVariable($name)}
try {
 $env:GOMAXPROCS='2';$env:GOFLAGS='-mod=readonly';$env:GOTOOLCHAIN='local';$env:GOWORK='off';$env:TOOLNEST_ALLOW_FOREGROUND_TESTS=$null
 Push-Location $repository
 try {
  # 后台控制器只启动固定 worker；ToolNest 从本次新目录运行，数据与注册表均显式隔离。
  $runner=Join-Path $programs 'desktop-runner.exe';$application=Join-Path $programs 'ToolNest.exe'
  & $Go build -trimpath -buildvcs=false -o $runner ./tests/e2e/desktop-runner
  if($LASTEXITCODE){throw 'Background runner build failed'}
  $entryArguments=@()
  if($Scenario -in @('entry','update-ui')){
   if(!$PreviousApplication-or !$UpdatedApplication){throw 'Entry checks require both explicit TEST-ONLY application builds'}
   $fixtureIdentities=@()
   foreach($source in @($PreviousApplication,$UpdatedApplication)){
    $identity=& (Join-Path $repository 'scripts\Read-BuildInfo.ps1') -Path $source|ConvertFrom-Json
    if($identity.channel-cnotin @('test','manual-test')){throw 'Entry fixtures must be TEST-ONLY applications'}
    $fixtureIdentities+=@($identity)
   }
   $updatedDirectory=Join-Path $programs 'updated';[void][IO.Directory]::CreateDirectory($updatedDirectory)
   $newApplication=Join-Path $updatedDirectory 'ToolNest.exe'
   Copy-Item -LiteralPath $PreviousApplication -Destination $application
   Copy-Item -LiteralPath $UpdatedApplication -Destination $newApplication
   if($Scenario -eq 'entry'){
    $entryTests=Join-Path $programs 'entry.test.exe'
    & $Go test -c -mod=readonly -o $entryTests ./cmd/toolnest
    if($LASTEXITCODE){throw 'Entry tests compilation failed'}
    $entryArguments=@('--entry-tests',$entryTests,'--updated-application',$newApplication)
   }else{
    if(!$SignedUpdate){throw 'UI update requires pre-signed application fixtures'}
    $worker=Join-Path $PSScriptRoot 'Update-Background-Worker.ps1'
    $fixtureRunner=Join-Path $programs 'acceptance-runner.exe'
    & $Go build -trimpath -buildvcs=false -o $fixtureRunner ./tests/e2e/runner
    if($LASTEXITCODE){throw 'Loopback fixture compilation failed'}
    $entryArguments=@('--updated-application',$newApplication,'--fixture-runner',$fixtureRunner,'--signed-update',[IO.Path]::GetFullPath($SignedUpdate),'--baseline-version',$fixtureIdentities[0].version,'--target-version',$fixtureIdentities[1].version)
   }
  }else{
   & $Go build -trimpath -buildvcs=false -o $application .
   if($LASTEXITCODE){throw 'Application build failed'}
  }
  $output=Join-Path $run 'evidence'
  & $runner --application $application --worker $worker --out $output --scenario $Scenario --timeout ($TimeoutSeconds.ToString()+'s') @entryArguments
  if($LASTEXITCODE){throw ('Private desktop test failed; evidence: '+$output)}
  $isolation=Get-Content -LiteralPath (Join-Path $output 'isolation.json') -Raw|ConvertFrom-Json
  if(!$isolation.inputDesktopUnchanged-or $isolation.workerOnForeground-or $isolation.childrenRemaining-ne 0){throw 'Foreground or cleanup acceptance failed'}
  [pscustomobject]@{status='passed';scenario=$Scenario;evidence=$output;inputDesktopUnchanged=$true;testWindowsReachedForeground=$false}
 }finally{Pop-Location}
}finally{foreach($name in $saved.Keys){[Environment]::SetEnvironmentVariable($name,$saved[$name])}}
