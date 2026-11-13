#requires -Version 7.2
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Path)
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
$start=[Diagnostics.ProcessStartInfo]::new([IO.Path]::GetFullPath($Path),'--toolnest-build-info')
$start.UseShellExecute=$false;$start.CreateNoWindow=$true
$start.RedirectStandardOutput=$true;$start.RedirectStandardError=$true
$process=[Diagnostics.Process]::Start($start)
try {
    # PowerShell 对 GUI exe 的直接管道可能提前关闭；显式等待只读身份进程并持续读取两条管道。
    $stdout=$process.StandardOutput.ReadToEndAsync();$stderr=$process.StandardError.ReadToEndAsync()
    if(!$process.WaitForExit(10000)){throw 'Read-only build-info did not exit within 10 seconds; no process was force-closed.'}
    $text=$stdout.GetAwaiter().GetResult()
    $errorText=$stderr.GetAwaiter().GetResult()
    if($process.ExitCode-ne 0){throw ('Read-only build-info failed: '+$errorText)}
    $identity=$text|ConvertFrom-Json
    if(!$identity-or !$identity.PSObject.Properties['version']-or !$identity.PSObject.Properties['channel']){throw 'Executable returned no valid build identity.'}
    Write-Output $text
}finally{$process.Dispose()}
