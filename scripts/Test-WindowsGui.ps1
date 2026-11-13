#requires -Version 7.2
[CmdletBinding()]
param()
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
$directory=Join-Path ([IO.Path]::GetTempPath()) ('toolnest-pe-negative-'+[guid]::NewGuid().ToString('N'))
[void][IO.Directory]::CreateDirectory($directory)
$verified=0
foreach($magic in @(0x10B,0x20B)){
    foreach($subsystem in @(2,3)){
        # 只构造不可执行的 PE 头来验证判据，不启动程序、不访问真实安装位置。
        $bytes=[byte[]]::new(512)
        [BitConverter]::GetBytes([uint16]0x5A4D).CopyTo($bytes,0)
        [BitConverter]::GetBytes([uint32]128).CopyTo($bytes,0x3C)
        [BitConverter]::GetBytes([uint32]0x00004550).CopyTo($bytes,128)
        [BitConverter]::GetBytes([uint16]240).CopyTo($bytes,148)
        [BitConverter]::GetBytes([uint16]$magic).CopyTo($bytes,152)
        [BitConverter]::GetBytes([uint16]$subsystem).CopyTo($bytes,220)
        $path=Join-Path $directory ($magic.ToString()+'-'+$subsystem+'.exe')
        [IO.File]::WriteAllBytes($path,$bytes)
        $rejected=$false
        try{$null=& (Join-Path $PSScriptRoot 'Assert-WindowsGui.ps1') -Path $path}catch{
            if($_.Exception.Message-notlike '*must use Windows GUI subsystem*'){throw};$rejected=$true
        }
        if($rejected-ne ($subsystem-eq 3)){throw 'PE GUI subsystem check accepted console or rejected GUI.'}
        $verified++
    }
}
$invalid=Join-Path $directory 'invalid.exe'
[IO.File]::WriteAllBytes($invalid,[byte[]]::new(64))
$invalidRejected=$false
try{$null=& (Join-Path $PSScriptRoot 'Assert-WindowsGui.ps1') -Path $invalid}catch{
    if($_.Exception.Message-notlike '*DOS header*'){throw};$invalidRejected=$true
}
if(!$invalidRejected){throw 'Malformed executable passed GUI validation.'}
Write-Output ('PASS: '+$verified+' PE32/PE32+ GUI and console cases, plus malformed executable refusal; no application started.')
