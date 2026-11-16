#requires -Version 5.1
param([Parameter(Mandatory)][string]$Config)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$configuration=Get-Content -LiteralPath $Config -Raw -Encoding UTF8|ConvertFrom-Json
$result=@{status='failed';steps=@();scenario='installed-kit'}
try {
 Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes,System.Drawing
 Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class KitDesktop {
 public delegate bool EnumProc(IntPtr window,IntPtr state);
 [DllImport("user32.dll")] public static extern IntPtr GetThreadDesktop(uint thread);
 [DllImport("kernel32.dll")] public static extern uint GetCurrentThreadId();
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern bool GetUserObjectInformation(IntPtr handle,int index,System.Text.StringBuilder text,int size,out int needed);
 [DllImport("user32.dll")] public static extern bool EnumWindows(EnumProc callback,IntPtr parameter);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window,out uint pid);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
 [DllImport("user32.dll")] public static extern bool IsWindowEnabled(IntPtr window);
 [DllImport("user32.dll")] public static extern IntPtr GetDlgItem(IntPtr dialog,int identifier);
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr window,System.Text.StringBuilder text,int size);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr window,uint message,IntPtr first,IntPtr second);
 [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr window,uint message,IntPtr first,IntPtr second);
 [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr window,IntPtr dc,uint flags);
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window,out RECT rect);
 [StructLayout(LayoutKind.Sequential)] public struct RECT {public int Left,Top,Right,Bottom;}
 public static string Title(IntPtr window) {var text=new System.Text.StringBuilder(256);GetWindowText(window,text,text.Capacity);return text.ToString();}
}
'@
 $desktop=[KitDesktop]::GetThreadDesktop([KitDesktop]::GetCurrentThreadId())
 $name=[Text.StringBuilder]::new(256);$needed=0
 if(![KitDesktop]::GetUserObjectInformation($desktop,2,$name,512,[ref]$needed)-or $name.ToString()-cne $configuration.Desktop-or $name.ToString()-notmatch '^ToolNest\.Test\.\d+\.\d+$'){throw 'Installed kit worker is not on its independently created desktop'}
 if($configuration.Scenario-cne 'installed-kit'){throw 'Unexpected scenario'}
 $kit=Get-Content -LiteralPath $configuration.KitResult -Raw -Encoding UTF8|ConvertFrom-Json
 if($kit.kitID-cnotmatch '^[a-f0-9]{32}$'-or $kit.baseline-cne '0.1.5'-or $kit.target-cne '0.1.6'){throw 'Explicit supported manual kit identity required'}
 # 安装夹具与证据必须位于 runner 认可的系统临时验收根；旧版写死 F 盘会在本机 TEMP 下误拒绝。
 $reserved=Join-Path ([IO.Path]::GetTempPath()) 'toolnest-acceptance\'
 foreach($file in @($kit.baselineInstaller,$kit.targetInstaller,$kit.applicationBaseline,$kit.applicationTarget)){
  if(![IO.Path]::GetFullPath($file).StartsWith($reserved,[StringComparison]::OrdinalIgnoreCase)-or !(Test-Path -LiteralPath $file -PathType Leaf)){throw 'Untrusted kit input path'}
 }
 $local=[Environment]::GetFolderPath('LocalApplicationData')
 $program=Join-Path $local ('Programs\ToolNest-UpdateTest-'+$kit.kitID)
 $data=Join-Path $local ('ToolNest-UpdateTest\'+$kit.kitID+'\data')
 $source=Join-Path $local ('ToolNest-UpdateTest\'+$kit.kitID+'\source')
 $application=Join-Path $program 'ToolNest.exe';$launcher=Join-Path $program 'ToolNest-TestLauncher.exe'
 $registryRoot='Software\ToolNest\Tests\'+$kit.kitID
 $menu=Join-Path ([Environment]::GetFolderPath('Programs')) ('ToolNest 更新流程测试 '+$kit.kitID.Substring(0,8))
 $shortcut=Join-Path $menu 'ToolNest 更新测试版.lnk'
 $desktopShortcut=Join-Path ([Environment]::GetFolderPath('DesktopDirectory')) ('ToolNest 更新测试版 '+$kit.kitID.Substring(0,8)+'.lnk')
 $uninstallKey='Software\Microsoft\Windows\CurrentVersion\Uninstall\ToolNest.UpdateTest.'+$kit.kitID
 if(Test-Path -LiteralPath $program){
  if(Test-Path -LiteralPath (Join-Path $program 'kit.json')){
   $prior=Get-Content -LiteralPath (Join-Path $program 'kit.json') -Raw -Encoding UTF8|ConvertFrom-Json
   if($prior.ID-cne $kit.kitID){throw 'Existing install is not this explicit acceptance kit'}
  }else{
   $registration=[Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($uninstallKey)
   try{if(!$registration-or $registration.GetValue('InstallLocation')-ine $program-or @(Get-ChildItem -LiteralPath $program -Force|Where-Object {$_.Name-cnotin @('Uninstall.exe','.manual-maintenance.lock')}).Count){throw 'Unknown partial installation'}}
   finally{if($registration){$registration.Dispose()}}
  }
 }
 function Real-PathDigest {
  $values=@()
  foreach($target in @(@([Microsoft.Win32.Registry]::CurrentUser,'Environment'),@([Microsoft.Win32.Registry]::LocalMachine,'SYSTEM\CurrentControlSet\Control\Session Manager\Environment'))){
   $key=$target[0].OpenSubKey($target[1]);try{$values+=@{kind=$key.GetValueKind('Path').ToString();value=$key.GetValue('Path',$null,[Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)}}finally{if($key){$key.Dispose()}}
  }
  $hash=[Security.Cryptography.SHA256]::Create();try{return [Convert]::ToBase64String($hash.ComputeHash([Text.Encoding]::UTF8.GetBytes(($values|ConvertTo-Json -Depth 4 -Compress))))}finally{$hash.Dispose()}
 }
 $realBefore=Real-PathDigest
 function Wait-Check([scriptblock]$condition,[int]$seconds,[string]$description){
  $result.step=$description;$deadline=[DateTime]::UtcNow.AddSeconds($seconds)
  do{if(& $condition){return};Start-Sleep -Milliseconds 100}while([DateTime]::UtcNow-lt $deadline)
  throw ('Timeout: '+$description)
 }
 function Read-State([string]$path){if(!(Test-Path -LiteralPath $path)){return $null};try{return Get-Content -LiteralPath $path -Raw -Encoding UTF8|ConvertFrom-Json}catch{return $null}}
 function Hash([string]$path){$digest=[Security.Cryptography.SHA256]::Create();$stream=[IO.File]::OpenRead($path);try{return [BitConverter]::ToString($digest.ComputeHash($stream))}finally{$stream.Dispose();$digest.Dispose()}}
 function Run-Hidden([string]$file,[string]$arguments,[int]$seconds=45){
  $start=[Diagnostics.ProcessStartInfo]::new($file,$arguments);$start.UseShellExecute=$false;$start.CreateNoWindow=$true
  $start.RedirectStandardError=$true;$start.RedirectStandardOutput=$true
  $process=[Diagnostics.Process]::Start($start);$stderr=$process.StandardError.ReadToEndAsync();$stdout=$process.StandardOutput.ReadToEndAsync()
  if(!$process.WaitForExit($seconds*1000)){throw ('Process exceeded private deadline: '+[IO.Path]::GetFileName($file))}
  $code=$process.ExitCode;$errorText=$stderr.Result;$text=$stdout.Result;$process.Dispose()
  if($code-ne 0){throw ('Process failed: '+[IO.Path]::GetFileName($file)+' code='+$code+' '+$errorText+' '+$text)}
 }
 function Active-App {
  return @(Get-Process -Name ToolNest -ErrorAction SilentlyContinue|Where-Object {try{$_.Path-ieq $application}catch{$false}})
 }
 function Run-Wizard([string]$file,[string]$arguments='') {
  $start=[Diagnostics.ProcessStartInfo]::new($file,$arguments);$start.UseShellExecute=$false;$start.CreateNoWindow=$true
  $wizard=[Diagnostics.Process]::Start($start)
  $deadline=[DateTime]::UtcNow.AddSeconds(50);$pages=@()
  $script:wizardCandidates=@()
  do{
   $wizard.Refresh();if($wizard.HasExited){break}
   $script:wizardWindow=[IntPtr]::Zero
   $script:wizardCandidates=@()
   $callback=[KitDesktop+EnumProc]{param([IntPtr]$handle,[IntPtr]$state);[uint32]$owner=0;[void][KitDesktop]::GetWindowThreadProcessId($handle,[ref]$owner);if([KitDesktop]::IsWindowVisible($handle)){$script:wizardCandidates+=@{pid=$owner;title=[KitDesktop]::Title($handle)}};if($owner-eq $wizard.Id-and [KitDesktop]::IsWindowVisible($handle)-and [KitDesktop]::Title($handle)-like '*ToolNest*'){$script:wizardWindow=$handle};return $true}
   [void][KitDesktop]::EnumWindows($callback,[IntPtr]::Zero)
   if($script:wizardWindow-ne [IntPtr]::Zero){
    $root=[Windows.Automation.AutomationElement]::FromHandle($script:wizardWindow)
    $result.wizardControls=@($root.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition)|ForEach-Object {@{name=$_.Current.Name;role=[string]$_.Current.ControlType;enabled=$_.Current.IsEnabled}})
    $button=[KitDesktop]::GetDlgItem($script:wizardWindow,1)
    $label=[KitDesktop]::Title($button)
    if($button-ne [IntPtr]::Zero-and [KitDesktop]::IsWindowVisible($button)-and [KitDesktop]::IsWindowEnabled($button)-and $label-match '下一步|安装|卸载|完成|关闭'){
     # 私有桌面上的 NSIS 按钮可能被 UIA 识别为 Pane；只向已核实进程的 IDOK 发送点击通知，不激活窗口或注入键鼠。
     if(![KitDesktop]::PostMessage($script:wizardWindow,0x0111,[IntPtr]1,$button)){throw 'Installer button notification failed'}
     $pages+=@($label);Start-Sleep -Milliseconds 350
    }
   }
   Start-Sleep -Milliseconds 100
  }while([DateTime]::UtcNow-lt $deadline)
  if(!$wizard.HasExited-or $wizard.ExitCode-ne 0-or $pages.Count-lt 2){$result.wizardPID=$wizard.Id;$result.wizardCandidates=$script:wizardCandidates;throw ('Installer wizard incomplete: '+($pages-join ', '))}
  $result.wizardPages+=@{executable=[IO.Path]::GetFileName($file);buttons=$pages}
  $wizard.Dispose()
 }
 function Find-Window {
  $ids=@(Active-App|ForEach-Object Id);$script:window=[IntPtr]::Zero
  $callback=[KitDesktop+EnumProc]{param([IntPtr]$handle,[IntPtr]$state);[uint32]$owner=0;[void][KitDesktop]::GetWindowThreadProcessId($handle,[ref]$owner);if($owner-in $ids-and [KitDesktop]::IsWindowVisible($handle)-and [KitDesktop]::Title($handle)-ceq 'ToolNest 更新测试版'){$script:window=$handle};return $true}
  [void][KitDesktop]::EnumWindows($callback,[IntPtr]::Zero)
  return $script:window-ne [IntPtr]::Zero
 }
 function Controls {
  if(!(Find-Window)){return @()}
  try{
   $root=[Windows.Automation.AutomationElement]::FromHandle($script:window)
   return @($root.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition))
  }catch [Management.Automation.MethodInvocationException]{
   # 页面重绘可能使本次 UIA 快照失效；交还有截止时间的轮询，下一次重新取本进程窗口。
   if($_.Exception.InnerException-is [Windows.Automation.ElementNotAvailableException]){return @()}
   throw
  }
 }
 function Invoke-Control([string]$label){
  # 升级验收会同时运行旧版与新版，首次使用按钮按各自的可访问名称匹配。
  $labels=@($label)
  if($label-ceq '稍后再说'){$labels+=@('先看看')}
  if($label-ceq '保存并开始'){$labels+=@('开始使用')}
  Wait-Check {$script:matching=@(Controls|Where-Object {$_.Current.Name-cin $labels-and $_.Current.IsEnabled-and $_.Current.ControlType-ne [Windows.Automation.ControlType]::Text});$script:matching.Count-eq 1} 12 ('control: '+$label)
  $pattern=$null;$element=$script:matching[0]
  if($element.TryGetCurrentPattern([Windows.Automation.InvokePattern]::Pattern,[ref]$pattern)){$pattern.Invoke()}
  elseif($element.TryGetCurrentPattern([Windows.Automation.TogglePattern]::Pattern,[ref]$pattern)){$pattern.Toggle()}
  elseif($element.TryGetCurrentPattern([Windows.Automation.SelectionItemPattern]::Pattern,[ref]$pattern)){$pattern.Select()}
  else{throw ('Control cannot be invoked: '+$label)}
  Start-Sleep -Milliseconds 200
 }
 function Has-Text([string]$expression,[switch]$Visible){
  return @(Controls|Where-Object {
   if($_.Current.Name-cnotmatch $expression){return $false}
   if(!$Visible){return $true}
   # MyGo 的播报节点会保留最近一次警告，尺寸为 1×1；视觉恢复检查只判断实际显示的内容。
   $bounds=$_.Current.BoundingRectangle
   return !$_.Current.IsOffscreen-and $bounds.Width-gt 1-and $bounds.Height-gt 1
  }).Count-gt 0
 }
 function Open-App {
  $shell=New-Object -ComObject WScript.Shell;$link=$shell.CreateShortcut($shortcut)
  if($link.TargetPath-ine $launcher-or $link.Arguments-cne '--open'-or $link.IconLocation-notlike ($application+'*')){throw 'Shortcut target, isolated parameters or icon mismatch'}
  Run-Hidden $link.TargetPath $link.Arguments 15
  Wait-Check {Find-Window} 20 'installed shortcut launched private application'
 }
 function Exit-App {
  if(!(Find-Window)){throw 'No test app window to exit'}
  [void][KitDesktop]::PostMessage($script:window,0x0010,[IntPtr]::Zero,[IntPtr]::Zero)
  Invoke-Control '退出应用，停止应用内计划';Invoke-Control '确认关闭'
  Wait-Check {@(Active-App).Count-eq 0} 15 'ordinary application exit'
 }
 function Screenshot([string]$filename){
  if(!(Find-Window)){throw 'No private window to capture'}
  $rect=[KitDesktop+RECT]::new();[void][KitDesktop]::GetWindowRect($script:window,[ref]$rect)
  $bitmap=[Drawing.Bitmap]::new($rect.Right-$rect.Left,$rect.Bottom-$rect.Top);$graphics=[Drawing.Graphics]::FromImage($bitmap);$dc=$graphics.GetHdc()
  try{if(![KitDesktop]::PrintWindow($script:window,$dc,2)){throw 'Private PrintWindow failed'}}finally{$graphics.ReleaseHdc($dc);$graphics.Dispose()}
  try{$bitmap.Save((Join-Path $configuration.Root $filename),[Drawing.Imaging.ImageFormat]::Png)}finally{$bitmap.Dispose()}
 }
 function Fixture-User([string]$newValue=''){
  $key=[Microsoft.Win32.Registry]::CurrentUser.OpenSubKey(($registryRoot+'\user'),($newValue-ne ''))
  if(!$key){throw 'Missing explicit test registry'}
  try{if($newValue-ne ''){$key.SetValue('Path',$newValue,[Microsoft.Win32.RegistryValueKind]::ExpandString)};return $key.GetValue('Path',$null,[Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)}finally{$key.Dispose()}
 }
 function Add-Step([string]$name){
  $result.steps+=@{name=$name;status='passed'}
  $result|ConvertTo-Json -Depth 8|Set-Content -LiteralPath (Join-Path $configuration.Root 'progress.json') -Encoding UTF8
 }
 # NSIS 安装页的部分 UIA 节点在 Windows PowerShell 5.1 下不会展开 ControlType，诊断字段用安全字符串。
 function Uninstall-Kit([switch]$Wizard) {
  # _?= 适用于外部副本；从原位置执行会让卸载器自身占用 Delete 目标，产生测试伪失败。
  $copy=Join-Path $configuration.Root ('uninstaller-'+[guid]::NewGuid().ToString('N')+'.exe')
  Copy-Item -LiteralPath (Join-Path $program 'Uninstall.exe') -Destination $copy
  if($Wizard){Run-Wizard $copy ('_?='+$program)}else{Run-Hidden $copy ('/S _?='+$program)}
 }
 # 只允许重新验收本轮明确构建的组合；旧轮数据改名保留，绝不清理用户正在使用的组合。
 if(Test-Path -LiteralPath (Join-Path $program 'ToolNest-TestLauncher.exe')){Uninstall-Kit}
 if((Test-Path -LiteralPath $program)-and !(Test-Path -LiteralPath (Join-Path $program 'kit.json'))){
  if([IO.Path]::GetFullPath($program)-cne [IO.Path]::GetFullPath((Join-Path $local ('Programs\ToolNest-UpdateTest-'+$kit.kitID)))){throw 'Unexpected archive path'}
  if((Get-Item -LiteralPath $program -Force).Attributes-band[IO.FileAttributes]::ReparsePoint){throw 'Redirected archive refused'}
  Rename-Item -LiteralPath $program -NewName ([IO.Path]::GetFileName($program)+'-prior-'+[guid]::NewGuid().ToString('N').Substring(0,8))
 }
 $kitDataRoot=Split-Path $data -Parent
 if(Test-Path -LiteralPath $kitDataRoot){
  $expected=Join-Path $local ('ToolNest-UpdateTest\'+$kit.kitID)
  if([IO.Path]::GetFullPath($kitDataRoot)-cne [IO.Path]::GetFullPath($expected)){throw 'Unexpected archive directory'}
  if((Get-Item -LiteralPath $kitDataRoot -Force).Attributes-band[IO.FileAttributes]::ReparsePoint){throw 'Refuse redirected test data'}
  Rename-Item -LiteralPath $kitDataRoot -NewName ($kit.kitID+'-prior-'+[guid]::NewGuid().ToString('N').Substring(0,8))
 }
 $result.step='install baseline with actual NSIS wizard'
 $result.wizardPages=@()
 Run-Wizard $kit.baselineInstaller
 if(!(Test-Path -LiteralPath $application)-or !(Test-Path -LiteralPath $shortcut)-or !(Test-Path -LiteralPath $desktopShortcut)){throw 'Installer did not create executable and searchable shortcuts'}
 $registration=[Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($uninstallKey)
 if(!$registration){throw 'Missing uninstall registration'}
 try{if($registration.GetValue('DisplayVersion')-cne $kit.baseline){throw 'Wrong installed baseline'}}finally{$registration.Dispose()}
 if((Hash $application)-cne (Hash $kit.applicationBaseline)){throw 'Installed payload differs from built payload'}
 foreach($file in @($application,$launcher,$kit.baselineInstaller,(Join-Path $program 'Uninstall.exe'))){$icon=[Drawing.Icon]::ExtractAssociatedIcon($file);if(!$icon){throw 'Executable icon missing'};$icon.Dispose()}
 Add-Step 'actual installation, payload hash, product icons and desktop/Start Menu shortcuts'
 Open-App
 Wait-Check {Has-Text '^开始使用 ToolNest$'} 10 'first-use onboarding'
 if([KitDesktop]::SendMessage($script:window,0x007F,[IntPtr]1,[IntPtr]::Zero)-eq [IntPtr]::Zero){throw 'Taskbar/window icon missing'}
 Invoke-Control '稍后再说'
 if(!(Test-Path -LiteralPath (Join-Path $data 'snapshots') -PathType Container)-or @(Get-ChildItem -LiteralPath (Join-Path $data 'snapshots') -File).Count-ne 0){throw 'Fresh skipped onboarding must create an empty readable store'}
 if(Has-Text '快照目录不可读取'){throw 'Original first-launch bug still present'}
 Screenshot 'first-use.png'
 Invoke-Control '应用设置';Invoke-Control '更新'
 Run-Hidden $launcher '--set-mode source-not-found';Start-Sleep -Milliseconds 500
 Invoke-Control '检查更新';Wait-Check {Has-Text 'HTTP 404'} 15 'actionable update source error'
 Run-Hidden $launcher '--set-mode good';Start-Sleep -Milliseconds 500
 Invoke-Control '检查更新'
 Wait-Check {Has-Text '^发现新版本$'} 15 'installed app found target'
 Invoke-Control '下载更新';Wait-Check {Has-Text '^更新已就绪$'} 40 'verified update before first backup'
 $statsBefore=(Read-State (Join-Path $source 'stats.json')).payloadRequests
 Invoke-Control '检查更新';Start-Sleep -Milliseconds 700
 if((Read-State (Join-Path $source 'stats.json')).payloadRequests-ne $statsBefore){throw 'Prepared update downloaded twice'}
 Invoke-Control '重启更新…';Invoke-Control '稍后更新'
 Exit-App
 if((Hash $application)-cne (Hash $kit.applicationBaseline)){throw 'Ordinary exit replaced installed baseline'}
 Add-Step 'first use without backup, signed download, repeat-check reuse, deferred ordinary exit'
 Open-App
 Wait-Check {$state=Read-State (Join-Path $data 'updates\switch.json');$state-and $state.phase-ceq 'confirmed'} 40 'new installed version confirms without first snapshot'
 Wait-Check {Find-Window} 15 'upgraded installed window'
 if((Hash $application)-cne (Hash $kit.applicationTarget)){throw 'Upgrade did not install the expected target'}
 Add-Step 'actual installed next-startup update with no snapshot yet'
 Invoke-Control 'PATH 保护';Invoke-Control '立即备份'
 # 结果标题可能因错误优先被折叠；底栏保留真实结果与直达入口，验收按该入口确认。
 Wait-Check {(Has-Text '^快照已保存$')-or (Has-Text '^查看本次快照$' -Visible)} 12 'verified backup result'
 Invoke-Control '查看本次快照'
 $initial=Fixture-User
 $changed='C:\ToolNestDemo\Changed;C:\ToolNestDemo\ChangedAgain'
 [void](Fixture-User $changed)
 Invoke-Control 'PATH 保护';Invoke-Control '刷新 PATH'
 Wait-Check {Has-Text 'ToolNestDemo\\Changed'} 10 'synthetic PATH refresh'
 Invoke-Control '查看本次快照';Invoke-Control '恢复快照…'
 Invoke-Control '确认恢复所选范围'
 Wait-Check {(Fixture-User)-ceq $initial} 12 'restored original synthetic bytes'
 Invoke-Control '查看恢复结果'
 # 点击底栏入口后结果详情是模态；范围核对文字应以该真实结果为准。
 Wait-Check {Has-Text '用户 PATH：已恢复并核对'} 8 'restore result scope verification'
 Screenshot 'restore-result.png'
 Invoke-Control '撤回这次恢复…'
 if((Fixture-User)-cne $initial){throw 'Undo wrote before a fresh confirmation'}
 Invoke-Control '确认恢复所选范围'
 Wait-Check {(Fixture-User)-ceq $changed} 12 'undo restores protection snapshot'
 Add-Step 'backup result, direct snapshot view, confirmed restore, protection snapshot and confirmed undo'
 $recordsFile=Join-Path $data 'records.json';$snapshotDirectory=Join-Path $data 'snapshots'
 $offlineName='snapshots-offline-'+[guid]::NewGuid().ToString('N').Substring(0,8)
 if([IO.Path]::GetFullPath($snapshotDirectory)-cne [IO.Path]::GetFullPath((Join-Path $data 'snapshots'))-or (Get-Item -LiteralPath $snapshotDirectory -Force).Attributes-band[IO.FileAttributes]::ReparsePoint){throw 'Unsafe offline-directory probe'}
 Rename-Item -LiteralPath $snapshotDirectory -NewName $offlineName
 Invoke-Control 'PATH 保护';Invoke-Control '刷新 PATH'
 Wait-Check {Has-Text '^找不到快照目录$' -Visible} 10 'directory failure has actionable explanation'
 if(Test-Path -LiteralPath $snapshotDirectory){throw 'Failure silently created replacement directory'}
 Invoke-Control '检查数据目录'
 Rename-Item -LiteralPath (Join-Path $data $offlineName) -NewName 'snapshots'
 Invoke-Control '重新检查目录'
 Wait-Check {(Has-Text '^刷新完成$' -Visible)-and !(Has-Text '^找不到快照目录$' -Visible)-and !(Has-Text '^检查数据目录$' -Visible)} 10 'directory recovery clears active problem'
 Screenshot 'directory-recovered.png'
 $result.directoryRecovered=$true
 Add-Step 'HTTP 404 source recovery and missing-directory action/recheck without replacement data'
 $snapshotHashes=@{};foreach($file in Get-ChildItem -LiteralPath $snapshotDirectory -Filter '*.json' -File){$snapshotHashes[$file.Name]=Hash $file.FullName}
 if($snapshotHashes.Count-lt 3){throw 'Backup plus both protection snapshots were not retained'}
 Exit-App
 $recordsBefore=Hash $recordsFile
 Run-Hidden $kit.baselineInstaller '/S'
 if((Hash $application)-cne (Hash $kit.applicationBaseline)){throw 'Overwrite install failed'}
 Open-App
 if(Has-Text '^开始使用 ToolNest$'){throw 'Reinstall lost onboarding state'}
 Invoke-Control '应用设置';Invoke-Control '更新';Invoke-Control '检查更新';Wait-Check {Has-Text '^发现新版本$'} 15 'new check after reinstall'
 Invoke-Control '下载更新';Wait-Check {Has-Text '^更新已就绪$'} 40 'second verified update'
 Invoke-Control '重启更新…';Invoke-Control '重启更新'
 Wait-Check {$state=Read-State (Join-Path $data 'updates\switch.json');$state-and $state.phase-ceq 'confirmed'-and (Hash $application)-ceq (Hash $kit.applicationTarget)} 45 'explicit restart confirms installed target'
 Wait-Check {Find-Window} 15 'restarted installed window'
 foreach($file in $snapshotHashes.Keys){if((Hash (Join-Path $snapshotDirectory $file))-cne $snapshotHashes[$file]){throw 'Snapshot bytes changed across upgrade'}}
 if((Fixture-User)-cne $changed){throw 'Upgrade changed synthetic PATH'}
 Add-Step 'overwrite baseline, explicit restart update, settings and exact snapshot preservation'
 Exit-App
 $recordsBefore=Hash $recordsFile
 Uninstall-Kit -Wizard
 if((Test-Path -LiteralPath $application)-or (Test-Path -LiteralPath $shortcut)-or (Test-Path -LiteralPath $desktopShortcut)){throw 'Uninstaller left installed app or shortcut'}
 if((Hash $recordsFile)-cne $recordsBefore){throw 'Uninstaller changed execution records'}
 foreach($file in $snapshotHashes.Keys){if((Hash (Join-Path $snapshotDirectory $file))-cne $snapshotHashes[$file]){throw 'Uninstaller changed snapshot'}}
 Add-Step 'actual uninstall removes program/shortcuts and preserves data'
 Run-Hidden $kit.baselineInstaller '/S'
 Open-App
 if(Has-Text '^开始使用 ToolNest$'){throw 'Reinstall reset first-use preferences'}
 if((Fixture-User)-cne $changed){throw 'Reinstall reset synthetic PATH'}
 foreach($file in $snapshotHashes.Keys){if((Hash (Join-Path $snapshotDirectory $file))-cne $snapshotHashes[$file]){throw 'Reinstall lost snapshot'}}
 Exit-App
 Uninstall-Kit
 Add-Step 'actual reinstall restores previous data and clean final uninstall'
 if([IO.Path]::GetFullPath((Split-Path $data -Parent))-cne [IO.Path]::GetFullPath((Join-Path $local ('ToolNest-UpdateTest\'+$kit.kitID)))){throw 'Unexpected completed data path'}
 Rename-Item -LiteralPath (Split-Path $data -Parent) -NewName ($kit.kitID+'-completed-'+[guid]::NewGuid().ToString('N').Substring(0,8))
 Run-Hidden $kit.baselineInstaller '/S'
 Open-App;Invoke-Control '保存并开始'
 # 首次备份的可见回执是完整说明；精确标题也可能被状态栏折叠，直达入口才是稳定完成信号。
 Wait-Check {(Has-Text '^第一份快照已保存')-or (Has-Text '^查看本次快照$' -Visible)} 15 'first-use save creates verified initial backup'
 if(@(Get-ChildItem -LiteralPath (Join-Path $data 'snapshots') -Filter '*.json' -File).Count-ne 1){throw 'First-use initial backup count mismatch'}
 Invoke-Control '查看本次快照'
 Screenshot 'first-backup.png'
 Exit-App;Uninstall-Kit
 Add-Step 'fresh installation onboarding creates first backup and direct snapshot entry'
 $realAfter=Real-PathDigest
 if($realBefore-cne $realAfter){throw 'Real PATH changed during acceptance'}
 $result.realPATHUnchanged=$true;$result.snapshotCount=$snapshotHashes.Count;$result.kitID=$kit.kitID
 $result.status='passed'
}catch{
 $result.error=$_.Exception.ToString();$result.errorLocation=$_.ScriptStackTrace
 try{@(Controls|ForEach-Object {@{name=$_.Current.Name;role=[string]$_.Current.ControlType;offscreen=$_.Current.IsOffscreen;bounds=$_.Current.BoundingRectangle.ToString()}})|ConvertTo-Json -Depth 4|Set-Content -LiteralPath (Join-Path $configuration.Root 'failure-controls.json') -Encoding UTF8}catch{}
 try{Screenshot 'failure.png'}catch{}
}finally{
 $result|ConvertTo-Json -Depth 8|Set-Content -LiteralPath (Join-Path $configuration.Root 'worker.json') -Encoding UTF8
}
if($result.status-ne 'passed'){exit 1}
