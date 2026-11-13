#requires -Version 5.1
param([Parameter(Mandatory)][string]$Config)
$ErrorActionPreference='Stop'
Set-StrictMode -Version Latest
$configuration=Get-Content -LiteralPath $Config -Raw -Encoding UTF8|ConvertFrom-Json
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class SandboxDesktop {
 [DllImport("user32.dll")] public static extern IntPtr GetThreadDesktop(uint thread);
 [DllImport("kernel32.dll")] public static extern uint GetCurrentThreadId();
 [DllImport("user32.dll",CharSet=CharSet.Unicode)] public static extern bool GetUserObjectInformation(IntPtr handle,int index,System.Text.StringBuilder text,int size,out int needed);
}
'@
$desktop=[SandboxDesktop]::GetThreadDesktop([SandboxDesktop]::GetCurrentThreadId())
$name=[Text.StringBuilder]::new(256);$needed=0
# 门禁失败时不进入结果写入和启动逻辑，伪造配置也不能在宿主产生副作用。
if(![SandboxDesktop]::GetUserObjectInformation($desktop,2,$name,512,[ref]$needed)-or $name.ToString()-cne $configuration.Desktop-or $name.ToString()-notmatch '^ToolNest\.Test\.\d+\.\d+$'){throw 'Sandbox worker is not on its independently created desktop'}
if($configuration.Scenario-cne 'sandbox'){throw 'Unexpected sandbox worker scenario'}
$result=@{status='failed';scenario='sandbox';guestStarted=$false;installersExecutedOnHost=$false}
try {
 $result.desktop=$name.ToString()
 $sandbox=Join-Path $env:windir 'System32\WindowsSandbox.exe'
 if(!(Test-Path -LiteralPath $sandbox -PathType Leaf)){throw 'Windows Sandbox launcher is missing'}
 # 只启动系统沙箱客户端；安装器仍由客体脚本的实际账户和固定映射目录双重门禁保护。
 $start=[Diagnostics.ProcessStartInfo]::new($sandbox,'"'+$configuration.SandboxConfig+'"')
 $start.UseShellExecute=$false;$start.CreateNoWindow=$true
 $start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
 $process=[Diagnostics.Process]::Start($start)
 $stdout=$process.StandardOutput.ReadToEndAsync();$stderr=$process.StandardError.ReadToEndAsync()
 $result.launcherPID=$process.Id
 $completion=Join-Path $configuration.Root 'guest-result.json'
 # 在外层 Job 截止前留出写证据时间；超时代表环境受阻，不伪装成产品验收失败。
 $deadline=[DateTime]::UtcNow.AddSeconds([Math]::Max(1,$configuration.TimeoutSeconds-8))
 do {
  if(Test-Path -LiteralPath $completion){
   $guest=Get-Content -LiteralPath $completion -Raw -Encoding UTF8|ConvertFrom-Json
   $result.guestStarted=$true;$result.status=$guest.status;$result.guestResults=$guest.results
   break
  }
  $process.Refresh()
  if($process.HasExited){
   $result.status='blocked';$result.exitCode=$process.ExitCode
   $result.exitHex='0x'+([BitConverter]::ToUInt32([BitConverter]::GetBytes([int]$process.ExitCode),0)).ToString('x8')
   $result.reason='Sandbox exited before the guest logon evidence; no host installer fallback'
   break
  }
  Start-Sleep -Milliseconds 250
 }while([DateTime]::UtcNow-lt $deadline)
 if($result.status-eq 'failed'){$result.status='blocked';$result.reason='No bounded guest completion; private desktop job will clean up only its own processes'}
 if($stdout.IsCompleted){[IO.File]::WriteAllText((Join-Path $configuration.Root 'sandbox.stdout.txt'),$stdout.Result,[Text.Encoding]::UTF8)}
 if($stderr.IsCompleted){[IO.File]::WriteAllText((Join-Path $configuration.Root 'sandbox.stderr.txt'),$stderr.Result,[Text.Encoding]::UTF8)}
}catch{$result.error=$_.Exception.Message;$result.errorLocation=$_.ScriptStackTrace}
finally{$result|ConvertTo-Json -Depth 12|Set-Content -LiteralPath (Join-Path $configuration.Root 'worker.json') -Encoding UTF8}
if($result.status-eq 'failed'){exit 1}
