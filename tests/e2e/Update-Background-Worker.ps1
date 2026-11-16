#requires -Version 5.1
param([Parameter(Mandatory)][string]$Config)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$configuration=Get-Content -LiteralPath $Config -Raw -Encoding UTF8|ConvertFrom-Json
$result=@{status='failed';scenario=$configuration.Scenario;protocols=@()}
$fixture=$null
$script:applicationLogs=@()
try {
 Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes
 Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class UpdateDesktop {
 public delegate bool EnumProc(IntPtr window,IntPtr state);
 [DllImport("user32.dll")] public static extern IntPtr GetThreadDesktop(uint thread);
 [DllImport("kernel32.dll")] public static extern uint GetCurrentThreadId();
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern bool GetUserObjectInformation(IntPtr handle,int index,System.Text.StringBuilder text,int size,out int needed);
 [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc callback,IntPtr parameter);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window,out uint pid);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr window,System.Text.StringBuilder text,int size);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window,uint message,IntPtr first,IntPtr second);
 [DllImport("kernel32.dll")] public static extern IntPtr OpenProcess(uint access,bool inherit,uint pid);
 [DllImport("kernel32.dll",CharSet=CharSet.Unicode)] public static extern bool QueryFullProcessImageName(IntPtr process,uint flags,System.Text.StringBuilder text,ref uint size);
 [DllImport("kernel32.dll")] public static extern bool CloseHandle(IntPtr handle);
 public static string ProcessPath(uint pid) {
  IntPtr process=OpenProcess(0x1000,false,pid);
  if(process==IntPtr.Zero)return "";
  try {uint size=32768;var name=new System.Text.StringBuilder((int)size);return QueryFullProcessImageName(process,0,name,ref size)?name.ToString():"";}finally{CloseHandle(process);}
 }
 public static string WindowTitle(IntPtr window) {
  var title=new System.Text.StringBuilder(256);GetWindowText(window,title,title.Capacity);return title.ToString();
 }
}
'@
 $desktop=[UpdateDesktop]::GetThreadDesktop([UpdateDesktop]::GetCurrentThreadId())
 $name=[Text.StringBuilder]::new(256);$needed=0
 if(![UpdateDesktop]::GetUserObjectInformation($desktop,2,$name,512,[ref]$needed)-or $name.ToString()-cne $configuration.Desktop-or $name.ToString()-notmatch '^ToolNest\.Test\.\d+\.\d+$'){throw 'Update worker is not on its independently created desktop'}
 if($configuration.Scenario-cne 'update-ui'){throw 'Unexpected background update scenario'}
 $result.desktop=$name.ToString()
 function Wait-Check([scriptblock]$condition,[int]$seconds,[string]$description) {
  $deadline=[DateTime]::UtcNow.AddSeconds($seconds)
  do {if(& $condition){return};Start-Sleep -Milliseconds 100}while([DateTime]::UtcNow-lt $deadline)
  throw ('Timeout: '+$description)
 }
 function Digest([string]$path) {
  $hash=[Security.Cryptography.SHA256]::Create();$stream=[IO.File]::OpenRead($path)
  try{return [BitConverter]::ToString($hash.ComputeHash($stream))}finally{$hash.Dispose();$stream.Dispose()}
 }
 function Read-State([string]$path) {
  if(!(Test-Path -LiteralPath $path)){return $null}
  try{return [IO.File]::ReadAllText($path,[Text.Encoding]::UTF8)|ConvertFrom-Json}catch{return $null}
 }
 function Window-For([int]$processID) {
  $script:matchingWindow=[IntPtr]::Zero
  $callback=[UpdateDesktop+EnumProc]{param([IntPtr]$handle,[IntPtr]$state);[uint32]$owner=0;[void][UpdateDesktop]::GetWindowThreadProcessId($handle,[ref]$owner);if($owner-eq $processID-and [UpdateDesktop]::IsWindowVisible($handle)-and [UpdateDesktop]::WindowTitle($handle)-cmatch '^ToolNest( 更新测试版)?$'){$script:matchingWindow=$handle};return $true}
  [void][UpdateDesktop]::EnumWindows($callback,[IntPtr]::Zero)
  return $script:matchingWindow
 }
 function Controls {
  try{return @($script:uiRoot.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition))}
  catch [Management.Automation.MethodInvocationException]{
   $script:activeProcess.Refresh()
   if($script:activeProcess.HasExited){throw ('Application exited before UI action; exit='+$script:activeProcess.ExitCode)}
   $window=Window-For $script:activeProcess.Id
   if($window-eq [IntPtr]::Zero){throw 'Application has no visible window during UI action'}
   $script:activeWindow=$window
   $script:uiRoot=[Windows.Automation.AutomationElement]::FromHandle($window)
   return @($script:uiRoot.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition))
  }
 }
 function Invoke-Control([string]$label) {
  $labels=@($label)
  if($label-ceq '稍后再说'){$labels+=@('先看看')}
  # 新版设置把代理收进「高级 → 配置代理」；旧夹具仍可能直接使用旧版入口。
  if($label-ceq '公共代理'-and !(Has-Text '^公共代理$')){
   Invoke-Control '高级'
   if(Has-Text '^配置代理$'){Invoke-Control '配置代理'}
   return
  }
  if($label-ceq '更新'-and !(Has-Text '^更新$')){Invoke-Control '关于与更新';return}
  if($label-ceq '应用设置'-and !(Has-Text '^应用设置$')){Invoke-Control '设置';return}
  $result.step='invoke: '+$label
  $deadline=[DateTime]::UtcNow.AddSeconds(8)
  do{
   $matching=@(Controls|Where-Object {$_.Current.Name-cin $labels-and $_.Current.IsEnabled-and $_.Current.ControlType-ne [Windows.Automation.ControlType]::Text})
   if($matching.Count-eq 1){break};Start-Sleep -Milliseconds 100
  }while([DateTime]::UtcNow-lt $deadline)
  if($matching.Count-ne 1){throw ('Ambiguous or missing update control: '+$label)}
  $pattern=$null
  if($matching[0].TryGetCurrentPattern([Windows.Automation.InvokePattern]::Pattern,[ref]$pattern)){$pattern.Invoke()}
  elseif($matching[0].TryGetCurrentPattern([Windows.Automation.SelectionItemPattern]::Pattern,[ref]$pattern)){$pattern.Select()}
  elseif($matching[0].TryGetCurrentPattern([Windows.Automation.TogglePattern]::Pattern,[ref]$pattern)){$pattern.Toggle()}
  else{throw ('Update action unavailable: '+$label)}
  Start-Sleep -Milliseconds 180
 }
 function Set-Value([string]$label,[string]$value) {
  $result.step='edit: '+$label
  $matching=@(Controls|Where-Object {$_.Current.Name-ceq $label-and $_.Current.IsEnabled-and $_.Current.ControlType-ne [Windows.Automation.ControlType]::Text})
  if($matching.Count-ne 1){throw ('Ambiguous update field: '+$label)}
  $pattern=$null
  if(!$matching[0].TryGetCurrentPattern([Windows.Automation.ValuePattern]::Pattern,[ref]$pattern)-or $pattern.Current.IsReadOnly){throw ('Update field is not editable: '+$label)}
  $pattern.SetValue($value)
 }
 function Has-Text([string]$expression) {
  return @(Controls|Where-Object {$_.Current.Name-cmatch $expression}).Count-gt 0
 }
 function Start-Application([string]$application,[string]$arguments) {
  $start=[Diagnostics.ProcessStartInfo]::new($application,$arguments);$start.UseShellExecute=$false;$start.CreateNoWindow=$true
  $start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
  $script:activeProcess=[Diagnostics.Process]::Start($start)
  $script:applicationLogs+=@{process=$script:activeProcess;stdout=$script:activeProcess.StandardOutput.ReadToEndAsync();stderr=$script:activeProcess.StandardError.ReadToEndAsync()}
  Wait-Check { $script:activeWindow=Window-For $script:activeProcess.Id; $script:activeWindow-ne [IntPtr]::Zero } 15 'application window'
  $script:uiRoot=[Windows.Automation.AutomationElement]::FromHandle($script:activeWindow)
 }
 function Exit-Application {
  [void][UpdateDesktop]::PostMessage($script:activeWindow,0x0010,[IntPtr]::Zero,[IntPtr]::Zero)
  Start-Sleep -Milliseconds 200
  Invoke-Control '退出应用，停止应用内计划';Invoke-Control '确认关闭'
  if(!$script:activeProcess.WaitForExit(10000)){throw 'Updated application did not exit normally'}
 }
 $private=Join-Path $configuration.Root 'private-fixture'
 # 两条串行更新链可能超过 3 分钟；本地夹具保持到统计读取完成，避免收尾时服务先退出。
 $fixtureArguments='--out "'+$private+'" --archive "'+(Join-Path $configuration.SignedUpdate ('update-'+$configuration.TargetVersion+'.tar.gz'))+'" --signature-file "'+(Join-Path $configuration.SignedUpdate ('update-'+$configuration.TargetVersion+'.sig'))+'" --public-key-file "'+(Join-Path $configuration.SignedUpdate 'update.pub')+'" --version '+$configuration.TargetVersion+' --current-version '+$configuration.BaselineVersion+' --duration 12m'
 $fixtureStart=[Diagnostics.ProcessStartInfo]::new($configuration.FixtureRunner,$fixtureArguments)
 $fixtureStart.UseShellExecute=$false;$fixtureStart.CreateNoWindow=$true
 $fixtureStart.RedirectStandardOutput=$true;$fixtureStart.RedirectStandardError=$true
 $fixture=[Diagnostics.Process]::Start($fixtureStart)
 $fixtureOut=$fixture.StandardOutput.ReadToEndAsync();$fixtureError=$fixture.StandardError.ReadToEndAsync()
 $privateFile=Join-Path $private 'fixture-private.json'
 Wait-Check {$fixture.Refresh();if($fixture.HasExited){throw ('Loopback fixture exited before readiness: '+$fixtureError.Result)};Test-Path -LiteralPath $privateFile} 10 'signed loopback fixture'
 $connection=Read-State $privateFile
 foreach($endpoint in @($connection.feed,$connection.httpProxy,$connection.socksProxy)){
  $uri=[uri]$endpoint;if($uri.Host-cne '127.0.0.1'-or $uri.Port-le 0-or $uri.UserInfo){throw 'Fixture escaped numeric loopback'}
 }
 $expectedNewHash=Digest $configuration.UpdatedApplication
 foreach($protocol in @('http','socks5')){
  $workspace=Join-Path $configuration.Root $protocol
  $programDirectory=Join-Path $workspace 'programs';$data=Join-Path $workspace 'data'
  [void][IO.Directory]::CreateDirectory($programDirectory)
  $application=Join-Path $programDirectory 'ToolNest.exe'
  Copy-Item -LiteralPath $configuration.Application -Destination $application
  $identifier=[guid]::NewGuid().ToString('N')
  $baseArguments='--data-dir "'+$data+'" --test-root '+$identifier
  # 初次设置也只访问本地同版本源，不能让公开源的后台下载抢在测试策略之前生效。
  $bootstrap=([uri]::new([uri]$connection.feed,'/current')).AbsoluteUri
  $arguments=$baseArguments+' --test-update-source "'+$bootstrap+'" --test-update-key-file "'+(Join-Path $configuration.SignedUpdate 'update.pub')+'"'
  $settingsFile=Join-Path $data 'settings.json';$preparedFile=Join-Path $data 'updates\prepared.json'
  $journalFile=Join-Path $data 'updates\switch.json';$helperResult=Join-Path $data 'updates\helper-result.json'
  Start-Application $application $arguments
  Invoke-Control '稍后再说';Invoke-Control '应用设置';Invoke-Control '更新'
  $initial=Read-State $settingsFile
  if($initial.settings.AutoCheck){Invoke-Control '自动检查更新'}
  if($initial.settings.AutoDownload){Invoke-Control '自动下载更新'}
  Invoke-Control '保存设置'
  Wait-Check {$settings=Read-State $settingsFile;$settings-and !$settings.settings.AutoCheck-and !$settings.settings.AutoDownload} 8 'automatic update settings persisted'
  Exit-Application
  $arguments=$baseArguments+' --test-update-source "'+$connection.feed+'" --test-update-key-file "'+(Join-Path $configuration.SignedUpdate 'update.pub')+'"'
  Start-Application $application $arguments
  $oldPID=$script:activeProcess.Id;$oldHash=Digest $application
  Invoke-Control 'PATH 保护';Invoke-Control '立即备份'
  $snapshotsDirectory=Join-Path $data 'snapshots'
  Wait-Check {@(Get-ChildItem -LiteralPath $snapshotsDirectory -Filter '*.json' -File -ErrorAction SilentlyContinue).Count-eq 1} 8 'snapshot persisted'
  $snapshot=Get-ChildItem -LiteralPath $snapshotsDirectory -Filter '*.json' -File|Select-Object -First 1
  $snapshotHash=Digest $snapshot.FullName
  Invoke-Control '自动备份';Invoke-Control '启用自动备份';Invoke-Control '保存计划'
  Wait-Check {$settings=Read-State $settingsFile;$settings-and $settings.settings.ScheduleEnabled-and $settings.schedule.enabled-and [DateTimeOffset]::Parse($settings.schedule.next)-gt [DateTimeOffset]::UtcNow} 10 'backup schedule completed initial check'
  Invoke-Control '应用设置';Invoke-Control '公共代理';Invoke-Control '自定义'
  $proxy=if($protocol-eq 'http'){$connection.httpProxy}else{$connection.socksProxy}
  Set-Value '代理地址' $proxy;Set-Value '代理账号' $connection.username;Set-Value '代理密码' $connection.credential
  Invoke-Control '保存设置'
  Wait-Check {$settings=Read-State $settingsFile;$settings-and $settings.settings.ProxyURL-ceq $proxy-and $settings.credential_id} 8 'authenticated proxy credentials persisted'
  Invoke-Control '使用草稿测试连接';Wait-Check {Has-Text '^更新入口连接正常'} 12 'authenticated proxy connection'
  if(Test-Path -LiteralPath $preparedFile){throw 'Application prepared update without explicit download'}
  Invoke-Control '更新';Invoke-Control '检查更新';Wait-Check {Has-Text '^发现新版本$'} 12 'new release available'
  Invoke-Control '版本说明';Wait-Check {Has-Text ('^版本说明 · '+[regex]::Escape($configuration.TargetVersion)+'$')} 5 'release notes';Invoke-Control '关闭说明'
  Invoke-Control '下载更新';$result.step='wait: verified update ready';Wait-Check {Has-Text '^更新已就绪$'} 20 'download, verification and staging'
  $prepared=Read-State $preparedFile
  if(!$prepared-or $prepared.version-cne $configuration.TargetVersion-or (Digest $application)-cne $oldHash){throw 'Staging replaced application before user consent'}
  $preparedDirectory=$prepared.workDir
  Invoke-Control '检查更新'
  Wait-Check {Has-Text '^更新已就绪$'} 12 'recheck preserves prepared update'
  if((Read-State $preparedFile).workDir-cne $preparedDirectory){throw 'Rechecking downloaded the already prepared version again'}
  Invoke-Control '重启更新…';Invoke-Control '稍后更新'
  $script:activeProcess.Refresh()
  if($script:activeProcess.HasExited-or (Digest $application)-cne $oldHash){throw 'Deferring update restarted or replaced application'}
  $settingsBefore=Read-State $settingsFile
  $exitDidNotRestart=$false
  if($protocol-eq 'socks5'){
   Exit-Application
   Start-Sleep -Milliseconds 1200
   if((Digest $application)-cne $oldHash-or (Test-Path -LiteralPath $journalFile)){throw 'Ordinary exit applied or restarted the prepared update'}
   $running=@(Get-Process -Name ToolNest -ErrorAction SilentlyContinue|Where-Object {[UpdateDesktop]::ProcessPath($_.Id)-ieq $application})
   if($running.Count){throw 'Ordinary exit left or relaunched the application'}
   $exitDidNotRestart=$true
   # 仅在本次非输入桌面模拟下一次明确启动；旧进程会交给助手，再由新版完成首帧确认。
   $start=[Diagnostics.ProcessStartInfo]::new($application,$arguments);$start.UseShellExecute=$false;$start.CreateNoWindow=$true
   $start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
   $script:activeProcess=[Diagnostics.Process]::Start($start)
   $script:applicationLogs+=@{process=$script:activeProcess;stdout=$script:activeProcess.StandardOutput.ReadToEndAsync();stderr=$script:activeProcess.StandardError.ReadToEndAsync()}
  }else{
   Invoke-Control '重启更新…';Invoke-Control '重启更新'
   if(!$script:activeProcess.WaitForExit(15000)){throw 'Old application did not exit after update confirmation'}
  }
  Wait-Check {$journal=Read-State $journalFile;$journal-and $journal.phase-ceq 'confirmed'} 35 'new first-frame startup confirmation'
  Wait-Check {$report=Read-State $helperResult;$null-ne $report} 8 'helper completion'
  $report=Read-State $helperResult;if($report.error){throw ('Update helper failed: '+$report.error)}
  if((Digest $application)-cne $expectedNewHash){throw 'Confirmed executable differs from signed new build'}
  $journal=Read-State $journalFile
  if($journal.version-cne $configuration.TargetVersion-or $journal.previousVersion-cne $configuration.BaselineVersion-or $journal.target-ine $application){throw 'Unexpected update transaction target'}
  # 同进程还会有可见辅助窗口，必须以正式标题识别主窗口；只枚举私有桌面且不激活窗口。
  $script:newWindow=[IntPtr]::Zero;$script:newPID=0
  $script:updateWindowCandidates=@()
  $callback=[UpdateDesktop+EnumProc]{param([IntPtr]$handle,[IntPtr]$state);[uint32]$owner=0;[void][UpdateDesktop]::GetWindowThreadProcessId($handle,[ref]$owner);$path=[UpdateDesktop]::ProcessPath($owner);$visible=[UpdateDesktop]::IsWindowVisible($handle);$title=[UpdateDesktop]::WindowTitle($handle);$script:updateWindowCandidates+=@{pid=$owner;path=$path;visible=$visible;title=$title};if($visible-and $owner-ne $oldPID-and $path-ieq $application-and $title-cmatch '^ToolNest( 更新测试版)?$'){$script:newWindow=$handle;$script:newPID=$owner};return $true}
  Wait-Check {$script:updateWindowCandidates=@();[void][UpdateDesktop]::EnumWindows($callback,[IntPtr]::Zero);$script:newWindow-ne [IntPtr]::Zero} 10 'updated application visible on private desktop'
  $script:activeProcess=Get-Process -Id $script:newPID;$script:activeWindow=$script:newWindow
  $script:uiRoot=[Windows.Automation.AutomationElement]::FromHandle($script:activeWindow)
  $settingsAfter=Read-State $settingsFile
  foreach($field in @('ScheduleEnabled','ScheduleMode','IntervalHours','DailyHour','DailyMinute','SnapshotDir','ProxyMode','ProxyURL','ProxyUsername','OnboardingDone','AutoCheck','AutoDownload','LaunchAtLogin','StartInTray','CloseBehavior','Theme')){
   if($settingsBefore.settings.$field-cne $settingsAfter.settings.$field){throw ('Update changed persisted setting '+$field)}
  }
  foreach($field in @('enabled','mode','hours','hour','minute','revision','next')){if($settingsBefore.schedule.$field-cne $settingsAfter.schedule.$field){throw ('Update changed schedule '+$field)}}
  if($settingsBefore.credential_id-cne $settingsAfter.credential_id-or (Digest $snapshot.FullName)-cne $snapshotHash){throw 'Update changed credential reference or snapshot bytes'}
  Invoke-Control '应用设置';Invoke-Control '公共代理';Invoke-Control '使用草稿测试连接'
  Wait-Check {Has-Text '^更新入口连接正常'} 12 'new version reused persisted credentials'
  Exit-Application
  $result.protocols+=@{protocol=$protocol;status='passed';oldPID=$oldPID;newPID=$script:newPID;phase=$journal.phase;snapshotPreserved=$true;settingsPreserved=$true;schedulePreserved=$true;credentialsReloaded=$true;deferredWithoutRestart=$true;recheckReusedPrepared=$true;ordinaryExitWithoutRestart=$exitDidNotRestart;nextStartupApplied=($protocol-eq 'socks5');version=$configuration.TargetVersion;installed=$false}
 }
 $status=[Net.WebClient]::new()
 try{$status.Proxy=$null;$statistics=$status.DownloadString(([uri]::new([uri]$connection.feed,'/stats')).AbsoluteUri)|ConvertFrom-Json}finally{$status.Dispose()}
 $result.proxyStats=$statistics
 # HTTP 计数是请求数，SOCKS5 计数是认证隧道数；同一隧道可复用，不能要求每次 HTTP 请求都重新握手。
 if($statistics.httpRequests-lt 4-or $statistics.socksRequests-lt 1-or $statistics.leakedCredentials){throw 'Authenticated proxy requests or credential-isolation checks failed'}
 $result.status='passed'
}catch{
 $result.error=$_.Exception.ToString();$result.errorLocation=$_.ScriptStackTrace
 if(Get-Variable -Name updateWindowCandidates -Scope Script -ErrorAction SilentlyContinue){$result.windowCandidates=$script:updateWindowCandidates}
}
finally {
 $logIndex=0
 foreach($log in $script:applicationLogs){
  if($log.stdout.IsCompleted-and $log.stderr.IsCompleted){
   [IO.File]::WriteAllText((Join-Path $configuration.Root ('application-'+$logIndex+'.stderr.txt')),$log.stderr.Result,[Text.Encoding]::UTF8)
   $logIndex++
  }
 }
 $result|ConvertTo-Json -Depth 8|Set-Content -LiteralPath (Join-Path $configuration.Root 'worker.json') -Encoding UTF8
}
if($result.status-ne 'passed'){exit 1}
