#requires -Version 7.2
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Path)
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
$stream=[IO.File]::Open($Path,[IO.FileMode]::Open,[IO.FileAccess]::Read,[IO.FileShare]::ReadWrite)
$reader=[IO.BinaryReader]::new($stream)
try {
    if($stream.Length-lt 64-or $reader.ReadUInt16()-ne 0x5A4D){throw 'Expected a valid Windows executable DOS header.'}
    $stream.Position=0x3C
    $pe=[long]$reader.ReadUInt32()
    if($pe-lt 64-or $pe+24-gt $stream.Length){throw 'Invalid Windows executable PE header offset.'}
    $stream.Position=$pe
    if($reader.ReadUInt32()-ne 0x00004550){throw 'Invalid Windows executable PE signature.'}
    $stream.Position=$pe+20
    $optionalSize=$reader.ReadUInt16()
    if($optionalSize-lt 70-or $pe+24+$optionalSize-gt $stream.Length){throw 'Invalid Windows executable optional header size.'}
    $stream.Position=$pe+24
    if($reader.ReadUInt16()-notin @(0x10B,0x20B)){throw 'Unsupported Windows executable optional header.'}
    # 直接检查实际产物而非构建参数；控制台子系统即使在后台测试中没弹窗，也不能交给用户。
    $stream.Position=$pe+24+68
    $subsystem=$reader.ReadUInt16()
    if($subsystem-ne 2){throw "ToolNest must use Windows GUI subsystem (2); found $subsystem. Rebuild with -H=windowsgui."}
    Write-Output ('PASS: Windows GUI subsystem (2): '+$Path)
}finally{$reader.Dispose()}
