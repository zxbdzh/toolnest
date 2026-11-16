Unicode true
!pragma warning error all
; 正式卸载器在嵌入前签名，任何外部签名失败都必须终止构建。
!ifdef SIGN_UNINSTALLER
  !uninstfinalize 'pwsh.exe -NoProfile -File "${SIGN_UNINSTALLER}" -Path "%1"' = 0
!endif
!include "MUI2.nsh"
!include "LogicLib.nsh"

!ifndef VERSION
  !error "VERSION must come from packaging/VERSION"
!endif
!ifndef PAYLOAD
  !error "PAYLOAD is required"
!endif
!ifndef OUTPUT
  !error "OUTPUT is required"
!endif

Name "ToolNest"
OutFile "${OUTPUT}"
InstallDir "$LOCALAPPDATA\Programs\ToolNest"
RequestExecutionLevel user
ManifestSupportedOS all
ManifestDPIAware true
SetCompressor /SOLID lzma
VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "ToolNest"
VIAddVersionKey "FileDescription" "ToolNest per-user installer"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "ToolNest contributors"

!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\ToolNest"
!define OWNER "ToolNest.per-user.v1"
!define MUI_ABORTWARNING
!define MUI_ICON "${PAYLOAD}\ToolNest.ico"
!define MUI_UNICON "${PAYLOAD}\ToolNest.ico"
!insertmacro MUI_PAGE_WELCOME
; 不提供目录页：程序与默认/自定义数据目录必须保持隔离。
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"

Var LockHandle
Var Result

!macro Init PREFIX
Function ${PREFIX}.onInit
  SetShellVarContext current
  SetRegView 64
  StrCpy $INSTDIR "$LOCALAPPDATA\Programs\ToolNest"
  StrCpy $LockHandle 0
  InitPluginsDir
  ; 将编译时的可信助手解压至随机插件目录，不执行旧安装目录里的文件。
  SetOutPath $PLUGINSDIR
  File /oname=ToolNest-maintenance.exe "${PAYLOAD}\ToolNest-maintenance.exe"
FunctionEnd
Function ${PREFIX}.onGUIEnd
  ${If} $LockHandle != 0
    System::Call 'kernel32::CloseHandle(p $LockHandle)'
    StrCpy $LockHandle 0
  ${EndIf}
FunctionEnd
!macroend
!insertmacro Init ""
!insertmacro Init "un"

!macro AbortBusy
  MessageBox MB_ICONEXCLAMATION|MB_OK "ToolNest 正在运行、维护文件被占用或目录归属未通过检查。请先正常退出 ToolNest（包括托盘）后重试；安装器不会强制关闭应用。" /SD IDOK
  SetErrorLevel 10
  Abort
!macroend

!macro Acquire MODE
  ; 先验证固定路径和现有登记；未知目录或 junction 一律失败关闭。
  nsExec::ExecToStack '"$PLUGINSDIR\ToolNest-maintenance.exe" --mode ${MODE} --install-dir "$INSTDIR"'
  Pop $Result
  Pop $0
  ${If} $Result != 0
    !insertmacro AbortBusy
  ${EndIf}
  CreateDirectory $INSTDIR
  ; share=0 与 Go AcquireInstallation 完全相同，锁覆盖全部文件替换窗口。
  System::Call 'kernel32::CreateFileW(w "$INSTDIR\.toolnest-maintenance.lock", i 0xC0000000, i 0, p 0, i 4, i 0x80, p 0) p.r0'
  ${If} $0 == -1
    !insertmacro AbortBusy
  ${EndIf}
  StrCpy $LockHandle $0
  ; 再次检查未接入锁的旧版本；绝不 taskkill 或请求强制退出。
  nsExec::ExecToStack '"$PLUGINSDIR\ToolNest-maintenance.exe" --mode ${MODE} --install-dir "$INSTDIR"'
  Pop $Result
  Pop $0
  ${If} $Result != 0
    !insertmacro AbortBusy
  ${EndIf}
!macroend

Section "ToolNest"
  !insertmacro Acquire "check-install"
  SetOverwrite on
  SetOutPath $INSTDIR
  ClearErrors
  File "${PAYLOAD}\ToolNest.exe"
  File "${PAYLOAD}\ToolNest-maintenance.exe"
  File "${PAYLOAD}\release-identity.json"
  File /oname=ToolNest-LICENSE.txt "..\LICENSE"
  File /oname=ThirdParty-MyGo.txt "licenses\github.com_egoist_mygo.txt"
  File /oname=ThirdParty-purego.txt "licenses\github.com_ebitengine_purego.txt"
  File /oname=ThirdParty-typesetting.txt "licenses\github.com_go-text_typesetting.txt"
  File /oname=ThirdParty-x-image.txt "licenses\golang.org_x_image.txt"
  File /oname=ThirdParty-x-net.txt "licenses\golang.org_x_net.txt"
  File /oname=ThirdParty-x-sys.txt "licenses\golang.org_x_sys.txt"
  File /oname=ThirdParty-x-text.txt "licenses\golang.org_x_text.txt"
  File /oname=ThirdParty-MiSans.txt "licenses\MiSans.txt"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  ${If} ${Errors}
    MessageBox MB_ICONSTOP|MB_OK "程序文件写入失败。请检查文件占用、目录权限或磁盘空间后重试；没有强制关闭任何应用。" /SD IDOK
    SetErrorLevel 11
    Abort
  ${EndIf}
  ; 仅写 HKCU 卸载登记和本用户开始菜单；无 PATH、Run、计划任务写入。
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "ToolNest"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" '"$INSTDIR\ToolNest.exe",0'
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKCU "${UNINSTALL_KEY}" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallerOwner" "${OWNER}"
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1
  CreateDirectory "$SMPROGRAMS\ToolNest"
  CreateShortcut "$SMPROGRAMS\ToolNest\ToolNest.lnk" "$INSTDIR\ToolNest.exe" "" "$INSTDIR\ToolNest.exe" 0
  CreateShortcut "$DESKTOP\ToolNest.lnk" "$INSTDIR\ToolNest.exe" "" "$INSTDIR\ToolNest.exe" 0
  ; 安装完成不自动启动：用户自行决定首次启动与自启。
SectionEnd

Section "Uninstall"
  !insertmacro Acquire "check-uninstall"
  nsExec::ExecToStack '"$PLUGINSDIR\ToolNest-maintenance.exe" --mode remove-owned-run --install-dir "$INSTDIR"'
  Pop $Result
  Pop $0
  ${If} $Result != 0
    MessageBox MB_ICONSTOP|MB_OK "无法确认自启入口归属，卸载已停止；程序数据与快照没有被删除。" /SD IDOK
    SetErrorLevel 12
    Abort
  ${EndIf}
  ; 白名单逐文件删除，绝不递归删除安装目录或读取用户配置中的快照路径。
  ClearErrors
  Delete "$INSTDIR\ToolNest.exe"
  Delete "$INSTDIR\ToolNest-maintenance.exe"
  Delete "$INSTDIR\release-identity.json"
  Delete "$INSTDIR\ToolNest-LICENSE.txt"
  Delete "$INSTDIR\ThirdParty-MyGo.txt"
  Delete "$INSTDIR\ThirdParty-purego.txt"
  Delete "$INSTDIR\ThirdParty-typesetting.txt"
  Delete "$INSTDIR\ThirdParty-x-image.txt"
  Delete "$INSTDIR\ThirdParty-x-net.txt"
  Delete "$INSTDIR\ThirdParty-x-sys.txt"
  Delete "$INSTDIR\ThirdParty-x-text.txt"
  Delete "$INSTDIR\ThirdParty-MiSans.txt"
  Delete "$INSTDIR\Uninstall.exe"
  ${If} ${Errors}
    ; 文件仍被占用时保留卸载登记供重试，绝不延迟到重启删除。
    MessageBox MB_ICONSTOP|MB_OK "程序文件仍被占用或无法删除。请正常退出 ToolNest 后重试；卸载入口已保留，快照与设置没有被删除。" /SD IDOK
    SetErrorLevel 13
    Abort
  ${EndIf}
  Delete "$SMPROGRAMS\ToolNest\ToolNest.lnk"
  Delete "$DESKTOP\ToolNest.lnk"
  RMDir "$SMPROGRAMS\ToolNest"
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
  System::Call 'kernel32::CloseHandle(p $LockHandle)'
  StrCpy $LockHandle 0
  Delete "$INSTDIR\.toolnest-maintenance.lock"
  RMDir "$INSTDIR"
  ; %LOCALAPPDATA%\ToolNest、PathGuard、自定义目录和未知文件全部保留。
SectionEnd
