#requires -Version 5.1
param([Parameter(Mandatory)][string]$Config)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$configuration=Get-Content -LiteralPath $Config -Raw -Encoding UTF8|ConvertFrom-Json
$result=@{status='failed';scenario=$configuration.Scenario;desktop=$configuration.Desktop}
$application=$null
try {
 Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes,System.Drawing
 Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class BackgroundDesktop {
 public delegate bool EnumProc(IntPtr window,IntPtr parameter);
 [DllImport("user32.dll")] public static extern IntPtr GetThreadDesktop(uint thread);
 [DllImport("kernel32.dll")] public static extern uint GetCurrentThreadId();
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern bool GetUserObjectInformation(IntPtr handle,int index,System.Text.StringBuilder text,int size,out int needed);
 [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc callback,IntPtr parameter);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window,out uint pid);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
 [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr window,IntPtr dc,uint flags);
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window,out RECT rect);
 [DllImport("user32.dll")] public static extern bool GetClientRect(IntPtr window,out RECT rect);
 [DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr window);
 [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr window);
 [DllImport("user32.dll")] public static extern bool IsZoomed(IntPtr window);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window,uint message,IntPtr first,IntPtr second);
 [StructLayout(LayoutKind.Sequential)] public struct RECT {public int Left,Top,Right,Bottom;}
}
'@
 $desktop=[BackgroundDesktop]::GetThreadDesktop([BackgroundDesktop]::GetCurrentThreadId())
 $name=[Text.StringBuilder]::new(256);$needed=0
 if(![BackgroundDesktop]::GetUserObjectInformation($desktop,2,$name,512,[ref]$needed)-or $name.ToString() -cne $configuration.Desktop-or $name.ToString() -notmatch '^ToolNest\.Test\.\d+\.\d+$'){throw 'Worker is not on its independently created desktop'}
 if($configuration.Scenario -eq 'entry'){
  # 测试开关只在核对当前真实桌面后设置，测试程序本身再次核验，不能仅靠环境变量伪造隔离。
  $env:TOOLNEST_ALLOW_FOREGROUND_TESTS=$null
  $env:TOOLNEST_ENTRY_TEST_DESKTOP=$configuration.Desktop
  $env:TOOLNEST_ENTRY_TEST_EXE=$configuration.Application
  $env:TOOLNEST_ENTRY_TEST_NEW_EXE=$configuration.UpdatedApplication
  $env:GOMAXPROCS='2'
  $entryStart=[Diagnostics.ProcessStartInfo]::new($configuration.EntryTests,'-test.v -test.run ^Test(EntryPreparedUpdateAndRollback|TrayHiddenNativeWindow)$ -test.timeout 150s')
  $entryStart.UseShellExecute=$false;$entryStart.CreateNoWindow=$true
  $entryStart.RedirectStandardOutput=$true;$entryStart.RedirectStandardError=$true
  $entryProcess=[Diagnostics.Process]::Start($entryStart)
  $entryOutput=$entryProcess.StandardOutput.ReadToEndAsync();$entryError=$entryProcess.StandardError.ReadToEndAsync()
  if(!$entryProcess.WaitForExit(155000)){throw 'Entry update scenarios exceeded their private-desktop deadline'}
  [IO.File]::WriteAllText((Join-Path $configuration.Root 'entry-tests.txt'),$entryOutput.Result,[Text.Encoding]::UTF8)
  [IO.File]::WriteAllText((Join-Path $configuration.Root 'entry-stderr.txt'),$entryError.Result,[Text.Encoding]::UTF8)
  if($entryProcess.ExitCode-ne 0){throw 'Actual application update or rollback failed; see entry-tests.txt'}
  if($entryOutput.Result-notmatch '(?m)^--- PASS: TestEntryPreparedUpdateAndRollback '){throw 'Entry tests did not execute or were skipped'}
  if($entryOutput.Result-notmatch '(?m)^--- PASS: TestTrayHiddenNativeWindow '){throw 'Hidden native window test did not execute or was skipped'}
  $result.status='passed';$result.scenarios=@('confirmed','confirmed-in-tray','startup-failed-rollback','hidden-window-native-events')
  return
 }
 if($configuration.Scenario -eq 'inspect'){
  $nativeTests=Join-Path (Split-Path -Parent $configuration.Application) 'entry.test.exe'
  if(!(Test-Path -LiteralPath $nativeTests -PathType Leaf)){throw 'Native hidden-window test executable missing'}
  $env:TOOLNEST_ENTRY_TEST_DESKTOP=$configuration.Desktop
  $nativeStart=[Diagnostics.ProcessStartInfo]::new($nativeTests,'-test.v -test.run ^TestTrayHiddenNativeWindow$ -test.timeout 30s')
  $nativeStart.UseShellExecute=$false;$nativeStart.CreateNoWindow=$true
  $nativeStart.RedirectStandardOutput=$true;$nativeStart.RedirectStandardError=$true
  $nativeProcess=[Diagnostics.Process]::Start($nativeStart)
  $nativeOutput=$nativeProcess.StandardOutput.ReadToEndAsync();$nativeError=$nativeProcess.StandardError.ReadToEndAsync()
  if(!$nativeProcess.WaitForExit(35000)){throw 'Hidden-window regression exceeded its private-desktop deadline'}
  [IO.File]::WriteAllText((Join-Path $configuration.Root 'hidden-window-tests.txt'),$nativeOutput.Result+$nativeError.Result,[Text.Encoding]::UTF8)
  if($nativeProcess.ExitCode-ne 0-or $nativeOutput.Result-notmatch '(?m)^--- PASS: TestTrayHiddenNativeWindow '){throw 'Hidden-window native regression failed or was skipped'}
  $result.hiddenWindowNativeEventsPassed=$true
 }
 $data=Join-Path $configuration.Root 'data'
 $testRoot=[guid]::NewGuid().ToString('N')
 $testRegistry='Software\ToolNest\Tests\'+$testRoot
 $originalUser='C:\ToolNestFixture\Python;C:\ToolNestFixture\Go;;C:\ToolNestFixture\Python'
 $originalSystem='C:\ToolNestFixture\System;C:\ToolNestFixture\Git'
 function Set-FixturePath([string]$scope,[string]$value) {
  # 夹具只能写本次随机专用键，不能把测试路径传播到真实环境或桌面。
  if($testRoot -cnotmatch '^[a-f0-9]{32}$'-or $scope -cnotin @('user','system')){throw 'Invalid isolated registry target'}
  $key=[Microsoft.Win32.Registry]::CurrentUser.CreateSubKey($testRegistry+'\'+$scope)
  try{$key.SetValue('Path',$value,[Microsoft.Win32.RegistryValueKind]::ExpandString)}finally{$key.Dispose()}
 }
 function Read-FixturePath([string]$scope) {
  if($scope -cnotin @('user','system')){throw 'Invalid isolated registry scope'}
  $key=[Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($testRegistry+'\'+$scope)
  try{return $key.GetValue('Path',$null,[Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)}finally{if($key){$key.Dispose()}}
 }
 if($configuration.Scenario -eq 'inspect'){Set-FixturePath 'user' $originalUser;Set-FixturePath 'system' $originalSystem}
 $arguments='--data-dir "'+$data+'" --test-root '+$testRoot
 if($configuration.Scenario -eq 'smoke'){$arguments+=' --smoke'}
 $start=[Diagnostics.ProcessStartInfo]::new($configuration.Application,$arguments)
 $start.UseShellExecute=$false;$start.CreateNoWindow=$true
 $start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
 # 只给本次私有桌面子进程打开耗时记录，不修改宿主环境或程序默认行为。
 if($configuration.Scenario-eq 'inspect'){$start.EnvironmentVariables['MYGO_FRAME_STATS']='all'}
 $application=[Diagnostics.Process]::Start($start)
 $stdout=$application.StandardOutput.ReadToEndAsync();$stderr=$application.StandardError.ReadToEndAsync()
 $result.applicationPID=$application.Id
 if($configuration.Scenario -eq 'smoke'){
  if(!$application.WaitForExit(20000)){throw 'Background native smoke did not exit'}
  $result.stdout=$stdout.Result;$result.stderr=$stderr.Result
  if($application.ExitCode -ne 0){throw 'Background native smoke failed'}
 } else {
  $window=[IntPtr]::Zero;$deadline=[DateTime]::UtcNow.AddSeconds(20)
  do {
   $script:window=[IntPtr]::Zero
   $callback=[BackgroundDesktop+EnumProc]{param([IntPtr]$handle,[IntPtr]$parameter);[uint32]$owner=0;[void][BackgroundDesktop]::GetWindowThreadProcessId($handle,[ref]$owner);if($owner -eq $application.Id-and [BackgroundDesktop]::IsWindowVisible($handle)){$script:window=$handle};return $true}
   [void][BackgroundDesktop]::EnumWindows($callback,[IntPtr]::Zero)
   $window=$script:window
   if($window -ne [IntPtr]::Zero){break};Start-Sleep -Milliseconds 100
   $application.Refresh();if($application.HasExited){throw ('Application exited: '+$stderr.Result)}
  }while([DateTime]::UtcNow-lt $deadline)
  if($window -eq [IntPtr]::Zero){throw 'Independent desktop has no application window'}
  $windowThread=[BackgroundDesktop]::GetWindowThreadProcessId($window,[ref]([uint32]0))
  $appDesktop=[BackgroundDesktop]::GetThreadDesktop($windowThread)
  $appName=[Text.StringBuilder]::new(256)
  if(![BackgroundDesktop]::GetUserObjectInformation($appDesktop,2,$appName,512,[ref]$needed)-or $appName.ToString()-cne $configuration.Desktop){throw 'Application window escaped the private desktop'}
  $root=[Windows.Automation.AutomationElement]::FromHandle($window)
  $controls=$root.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition)
  $names=@();foreach($control in $controls){if($control.Current.Name){$names+=$control.Current.Name}}
  $result.controls=$names
  $skip=@($controls|Where-Object {$_.Current.Name-ceq '先看看'-and $_.Current.ControlType-ne [Windows.Automation.ControlType]::Text})
  if($skip.Count -eq 1){$skip[0].GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke();Start-Sleep -Milliseconds 400;$result.onboardingInvoked=$true}
  $bounds=[BackgroundDesktop+RECT]::new();[void][BackgroundDesktop]::GetWindowRect($window,[ref]$bounds)
  $bitmap=[Drawing.Bitmap]::new($bounds.Right-$bounds.Left,$bounds.Bottom-$bounds.Top);$graphics=[Drawing.Graphics]::FromImage($bitmap);$dc=$graphics.GetHdc()
  try{$captured=[BackgroundDesktop]::PrintWindow($window,$dc,2)}finally{$graphics.ReleaseHdc($dc)}
  try {if(!$captured){throw 'PrintWindow failed on the inactive desktop'};$bitmap.Save((Join-Path $configuration.Root 'window.png'),[Drawing.Imaging.ImageFormat]::Png)}finally{$graphics.Dispose();$bitmap.Dispose()}
  $result.capture='PrintWindow on private desktop; visual content must be reviewed'
  function Controls {
   $deadline=[DateTime]::UtcNow.AddSeconds(3)
   do {
    try {
     $freshRoot=[Windows.Automation.AutomationElement]::FromHandle($window)
     return @($freshRoot.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition))
    } catch [Management.Automation.MethodInvocationException] {
     # 仅重取已销毁的 UIA 节点，绝不自动重放点击，以免重复执行恢复或删除。
     if($_.Exception.InnerException -isnot [Windows.Automation.ElementNotAvailableException]-or [DateTime]::UtcNow-ge $deadline){throw}
     $application.Refresh();if($application.HasExited){throw 'Application exited while refreshing accessibility nodes'}
     Start-Sleep -Milliseconds 80
    }
   }while([DateTime]::UtcNow-lt $deadline)
   throw 'Accessibility snapshot did not settle within its deadline'
  }
  function Matching-Control([string]$label) {
   $deadline=[DateTime]::UtcNow.AddSeconds(5)
   do {
    try {
     $matching=@(Controls|Where-Object {$_.Current.Name-ceq $label-and $_.Current.ControlType-ne [Windows.Automation.ControlType]::Text-and $_.Current.IsEnabled})
     if($matching.Count-eq 1){return $matching[0]}
     if($matching.Count-gt 1){throw ('Ambiguous native control: '+$label)}
    } catch [Management.Automation.MethodInvocationException] {
     if($_.Exception.InnerException -isnot [Windows.Automation.ElementNotAvailableException]){throw}
    }
    Start-Sleep -Milliseconds 80
   }while([DateTime]::UtcNow-lt $deadline)
   throw ('Missing native control: '+$label)
  }
  function Invoke-Control([string]$label) {
   $result.step='invoke: '+$label
   $element=Matching-Control $label
   $pattern=$null
   if($element.TryGetCurrentPattern([Windows.Automation.TogglePattern]::Pattern,[ref]$pattern)){$pattern.Toggle()}
   elseif($element.TryGetCurrentPattern([Windows.Automation.InvokePattern]::Pattern,[ref]$pattern)){$pattern.Invoke()}
   elseif($element.TryGetCurrentPattern([Windows.Automation.SelectionItemPattern]::Pattern,[ref]$pattern)){$pattern.Select()}
   else{throw ('Native action unavailable: '+$label)}
   Start-Sleep -Milliseconds 350
  }
  function Set-ControlValue([string]$label,[string]$value) {
   $result.step='edit: '+$label
   $element=Matching-Control $label
   $pattern=$null
   if(!$element.TryGetCurrentPattern([Windows.Automation.ValuePattern]::Pattern,[ref]$pattern)-or $pattern.Current.IsReadOnly){throw ('Editable ValuePattern unavailable: '+$label)}
   $pattern.SetValue($value);Start-Sleep -Milliseconds 350
  }
  function Wait-SnapshotCount([int]$expected) {
   $deadline=[DateTime]::UtcNow.AddSeconds(10)
   do{
    $files=@(Get-ChildItem -LiteralPath (Join-Path $data 'snapshots') -Filter '*.json' -File -ErrorAction SilentlyContinue)
    if($files.Count-eq $expected){return $files}
    Start-Sleep -Milliseconds 100
   }while([DateTime]::UtcNow-lt $deadline)
   throw ('Unexpected snapshot count: '+$files.Count+'; expected '+$expected)
  }
  function Wait-EnabledControl([string]$label) {
   $deadline=[DateTime]::UtcNow.AddSeconds(10)
   do{
    $matching=@(Controls|Where-Object {$_.Current.Name-ceq $label-and $_.Current.IsEnabled-and $_.Current.ControlType-ne [Windows.Automation.ControlType]::Text})
    if($matching.Count-eq 1){return}
    Start-Sleep -Milliseconds 100
   }while([DateTime]::UtcNow-lt $deadline)
   throw ('Control did not become ready: '+$label)
  }
  function Snapshot-Digest([string]$path) {
   # 独立桌面的 PowerShell 5.1 不依赖模块自动加载，直接计算只读文件摘要。
   $algorithm=[Security.Cryptography.SHA256]::Create()
   $stream=[IO.File]::OpenRead($path)
   try{return [BitConverter]::ToString($algorithm.ComputeHash($stream))}finally{$stream.Dispose();$algorithm.Dispose()}
  }
  function Capture-Page([string]$label) {
   $surface=[BackgroundDesktop+RECT]::new();[void][BackgroundDesktop]::GetWindowRect($window,[ref]$surface)
   $image=[Drawing.Bitmap]::new($surface.Right-$surface.Left,$surface.Bottom-$surface.Top)
   $paint=[Drawing.Graphics]::FromImage($image);$target=$paint.GetHdc()
   try{$ok=[BackgroundDesktop]::PrintWindow($window,$target,2)}finally{$paint.ReleaseHdc($target)}
   try{if(!$ok){throw 'Native page capture failed'};$image.Save((Join-Path $configuration.Root ($label+'.png')),[Drawing.Imaging.ImageFormat]::Png)}finally{$paint.Dispose();$image.Dispose()}
  }
  function Wait-Theme([string]$theme) {
   $settingsFile=Join-Path $data 'settings.json'
   $deadline=[DateTime]::UtcNow.AddSeconds(10)
   do {
    if(Test-Path -LiteralPath $settingsFile){
     try{$settings=Get-Content -LiteralPath $settingsFile -Raw -Encoding UTF8|ConvertFrom-Json;if($settings.settings.Theme -ceq $theme){return}}catch{}
    }
    Start-Sleep -Milliseconds 100
   }while([DateTime]::UtcNow-lt $deadline)
   throw ('Theme was not persisted: '+$theme)
  }
  function Wait-Schedule([bool]$enabled) {
   $settingsFile=Join-Path $data 'settings.json'
   $deadline=[DateTime]::UtcNow.AddSeconds(10)
   do {
    if(Test-Path -LiteralPath $settingsFile){
     try{$settings=Get-Content -LiteralPath $settingsFile -Raw -Encoding UTF8|ConvertFrom-Json;if($settings.settings.ScheduleEnabled -eq $enabled){return}}catch{}
    }
    Start-Sleep -Milliseconds 100
   }while([DateTime]::UtcNow-lt $deadline)
   throw ('Schedule was not persisted: '+$enabled)
  }
  # UIA 调用软件自己绘制的按钮；不发送前台键鼠，也不切换输入桌面。
  Invoke-Control '最大化窗口'
  if(![BackgroundDesktop]::IsZoomed($window)){throw 'Custom maximize button did not maximize the native window'}
  Invoke-Control '还原窗口'
  if([BackgroundDesktop]::IsZoomed($window)){throw 'Custom restore button did not restore the native window'}
  Invoke-Control '最小化窗口'
  if(![BackgroundDesktop]::IsIconic($window)){throw 'Custom minimize button did not minimize the native window'}
  [void][BackgroundDesktop]::PostMessage($window,0x0112,[IntPtr]0xF120,[IntPtr]::Zero)
  Start-Sleep -Milliseconds 350
  if([BackgroundDesktop]::IsIconic($window)){throw 'Private test window did not restore after minimizing'}
  $outer=[BackgroundDesktop+RECT]::new();$client=[BackgroundDesktop+RECT]::new()
  if(![BackgroundDesktop]::GetWindowRect($window,[ref]$outer)-or ![BackgroundDesktop]::GetClientRect($window,[ref]$client)){throw 'Native frame dimensions unavailable'}
  $frameHeight=($outer.Bottom-$outer.Top)-($client.Bottom-$client.Top)
  $dpi=[BackgroundDesktop]::GetDpiForWindow($window)
  if($dpi-eq 0-or $frameHeight-gt [Math]::Ceiling(20*$dpi/96)){throw 'System title bar still consumes an extra row'}
  $result.customCaption=@{maximize=$true;restore=$true;minimize=$true;frameHeight=$frameHeight;dpi=$dpi;nativeTitlebarRemoved=$true}
  Invoke-Control '应用设置'
  Invoke-Control '浅色';Invoke-Control '保存设置';Wait-Theme 'light'
  Start-Sleep -Milliseconds 300
  Capture-Page 'settings-light'
  Invoke-Control '概览';Capture-Page 'home-empty-light'
  Invoke-Control '应用设置';Invoke-Control '高级';Invoke-Control '配置代理';Capture-Page 'proxy-settings'
  Invoke-Control '直连';Invoke-Control '保存设置'
  Invoke-Control '概览'
  Invoke-Control '备份当前环境'
  $homeSnapshots=@(Wait-SnapshotCount 1)
  $result.homeBackupInvoked=$true
  Capture-Page 'home-light'
  Invoke-Control 'PATH 保护'
  Set-ControlValue '用户 PATH搜索' 'Python';Capture-Page 'path-search'
  Set-ControlValue '用户 PATH搜索' ''
  Invoke-Control '立即备份'
  $snapshots=@(Wait-SnapshotCount 2)
  $result.backupInvoked=$true
  Capture-Page 'current'
  Invoke-Control '快照';Capture-Page 'snapshots'
  $saved=Get-Content -LiteralPath $homeSnapshots[0].FullName -Raw -Encoding UTF8|ConvertFrom-Json
  $snapshotHash=Snapshot-Digest $homeSnapshots[0].FullName
  Set-ControlValue '搜索快照' $saved.id
  Invoke-Control ('查看快照 '+$saved.id)
  Invoke-Control '与当前比较';Capture-Page 'snapshot-comparison'
  Invoke-Control '恢复快照…';Wait-EnabledControl '确认恢复所选范围';Capture-Page 'restore-preview'
  Invoke-Control '取消恢复'
  if((Read-FixturePath 'user')-cne $originalUser-or @(Wait-SnapshotCount 2).Count-ne 2){throw 'Cancelled restore mutated isolated state'}
  Set-FixturePath 'user' 'C:\ToolNestFixture\ChangedAfterBackup'
  Invoke-Control '刷新 PATH'
  Invoke-Control '恢复快照…';Wait-EnabledControl '确认恢复所选范围'
  Invoke-Control '确认恢复所选范围'
  $protected=@(Wait-SnapshotCount 3)
  $deadline=[DateTime]::UtcNow.AddSeconds(10)
  while((Read-FixturePath 'user')-cne $originalUser-and [DateTime]::UtcNow-lt $deadline){Start-Sleep -Milliseconds 100}
  if((Read-FixturePath 'user')-cne $originalUser-or (Read-FixturePath 'system')-cne $originalSystem){throw 'Confirmed user restore did not preserve both expected scopes'}
  if((Snapshot-Digest $homeSnapshots[0].FullName)-cne $snapshotHash){throw 'Restore modified original snapshot bytes'}
  $protection=@($protected|ForEach-Object {Get-Content -LiteralPath $_.FullName -Raw -Encoding UTF8|ConvertFrom-Json}|Where-Object reason -eq 'before-restore')
  if($protection.Count-ne 1){throw 'Restore omitted the protection snapshot'}
  $result.restoreCancelledWithoutWrite=$true;$result.userRestoreAndProtectionPassed=$true
  Invoke-Control ('选择快照 '+$saved.id)
  Invoke-Control '删除选中 (1)';Invoke-Control '取消'
  if(!(Test-Path -LiteralPath $homeSnapshots[0].FullName)){throw 'Cancelled deletion removed a snapshot'}
  Invoke-Control '删除选中 (1)';Invoke-Control '确认删除'
  [void](Wait-SnapshotCount 2)
  if(Test-Path -LiteralPath $homeSnapshots[0].FullName){throw 'Confirmed snapshot deletion did not remove the selected fixture'}
  $result.snapshotDeleteCancelledAndConfirmed=$true
  Invoke-Control '自动备份';Capture-Page 'schedule'
  Invoke-Control '启用自动备份';Capture-Page 'schedule-dirty'
  Invoke-Control '保存计划';Wait-Schedule $true
  Start-Sleep -Milliseconds 600
  Invoke-Control '应用设置'
  Invoke-Control '深色';Invoke-Control '保存设置';Wait-Theme 'dark'
  Start-Sleep -Milliseconds 300
  Capture-Page 'settings-dark'
  Invoke-Control 'PATH 保护';Capture-Page 'current-dark'
  Invoke-Control '快照';Capture-Page 'snapshots-dark'
  Invoke-Control '概览';Capture-Page 'home-dark'
  $result.pages=@('home-light','home-empty-light','home-dark','proxy-settings','path-search','current','snapshots','snapshots-dark','snapshot-comparison','restore-preview','schedule','schedule-dirty','settings-light','settings-dark','current-dark')
  $result.themesPersisted=@('light','dark')
  $result.scheduleSaved=$true
  Invoke-Control '关闭窗口'
  Start-Sleep -Milliseconds 250
  $controls=Controls
  $exit=@($controls|Where-Object {$_.Current.Name-ceq '退出应用，停止应用内计划'-and $_.Current.ControlType-ne [Windows.Automation.ControlType]::Text})
  $confirm=@($controls|Where-Object {$_.Current.Name-ceq '确认关闭'-and $_.Current.ControlType-ne [Windows.Automation.ControlType]::Text})
  if($exit.Count-ne 1-or $confirm.Count-ne 1){throw 'Cannot resolve normal-exit controls on private desktop'}
  $pattern=$null
  if($exit[0].TryGetCurrentPattern([Windows.Automation.SelectionItemPattern]::Pattern,[ref]$pattern)){$pattern.Select()}else{$exit[0].GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()}
  $confirm[0].GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern).Invoke()
  if(!$application.WaitForExit(10000)){throw 'Application did not exit normally'}
  $result.stdout=$stdout.Result;$result.stderr=$stderr.Result
  if($application.ExitCode-ne 0){throw 'Native background process failed on exit'}
  $trace=$stderr.Result
  [IO.File]::WriteAllText((Join-Path $configuration.Root 'native-frames.log'),$trace,[Text.Encoding]::UTF8)
  $frames=@([regex]::Matches($trace,'mygo: frame (?<frame>\d+) took (?<total>[\d.]+) ms: build (?<build>[\d.]+).*?layout (?<layout>[\d.]+), paint (?<paint>[\d.]+), present (?<present>[\d.]+) \((?<renderer>[^)]+)\)')|ForEach-Object {
   [pscustomobject]@{frame=[int]$_.Groups['frame'].Value;total=[double]::Parse($_.Groups['total'].Value,[Globalization.CultureInfo]::InvariantCulture);build=[double]::Parse($_.Groups['build'].Value,[Globalization.CultureInfo]::InvariantCulture);layout=[double]::Parse($_.Groups['layout'].Value,[Globalization.CultureInfo]::InvariantCulture);paint=[double]::Parse($_.Groups['paint'].Value,[Globalization.CultureInfo]::InvariantCulture);present=[double]::Parse($_.Groups['present'].Value,[Globalization.CultureInfo]::InvariantCulture);renderer=$_.Groups['renderer'].Value}
  })
  if($frames.Count-lt 20){throw 'Native frame statistics missing; motion performance is not verified'}
  $warm=@($frames|Select-Object -Skip 10)
  $totals=@($warm.total|Sort-Object);$cpu=@($warm|ForEach-Object {$_.build+$_.layout+$_.paint}|Sort-Object)
  $percentile95=[Math]::Min($totals.Count-1,[int][Math]::Ceiling($totals.Count*0.95)-1)
  $metrics=@{frames=$frames.Count;warmFrames=$warm.Count;p95TotalMs=$totals[$percentile95];maxTotalMs=$totals[-1];p95BuildLayoutPaintMs=$cpu[$percentile95];over50Ms=@($warm|Where-Object {$_.total-gt 50}).Count;renderers=@($frames.renderer|Select-Object -Unique);scope='private inactive desktop, includes captures and UIA; not physical display FPS'}
  $metrics|ConvertTo-Json -Depth 5|Set-Content -LiteralPath (Join-Path $configuration.Root 'native-frame-metrics.json') -Encoding UTF8
  $result.frameMetrics=$metrics
  # 完整耗时行单独归档，避免将大量诊断日志塞进界面验收摘要。
  $result.stderr=($trace-split "`n"|Where-Object {$_-notmatch 'mygo: frame '})-join "`n"
 }
 $result.status='passed'
}catch{
 $result.error=$_.Exception.ToString();$result.errorLocation=$_.ScriptStackTrace
 if($configuration.Scenario-ceq 'inspect'){
  try{Capture-Page 'failure'}catch{}
  try{@(Controls|ForEach-Object {@{name=$_.Current.Name;role=[string]$_.Current.ControlType;enabled=$_.Current.IsEnabled;offscreen=$_.Current.IsOffscreen}})|ConvertTo-Json -Depth 4|Set-Content -LiteralPath (Join-Path $configuration.Root 'failure-controls.json') -Encoding UTF8}catch{}
 }
}
finally {$result|ConvertTo-Json -Depth 8|Set-Content -LiteralPath (Join-Path $configuration.Root 'worker.json') -Encoding UTF8}
if($result.status-ne 'passed'){exit 1}
