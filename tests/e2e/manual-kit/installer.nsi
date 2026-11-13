Unicode true
!pragma warning error all
!include "MUI2.nsh"
!include "LogicLib.nsh"
Name "ToolNest 更新流程测试"
OutFile "${OUTPUT}"
InstallDir "$LOCALAPPDATA\Programs\ToolNest-UpdateTest-${KIT_ID}"
RequestExecutionLevel user
ManifestSupportedOS all
ManifestDPIAware true
SetCompressor /SOLID lzma
VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "ToolNest 更新流程测试"
VIAddVersionKey "FileDescription" "ToolNest isolated manual update test"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "ToolNest contributors"
!define KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\ToolNest.UpdateTest.${KIT_ID}"
!define MENU "ToolNest 更新流程测试 ${KIT_SHORT}"
!define MUI_ABORTWARNING
!define MUI_ICON "${PAYLOAD}\ToolNest.ico"
!define MUI_UNICON "${PAYLOAD}\ToolNest.ico"
!insertmacro MUI_PAGE_WELCOME
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
  StrCpy $INSTDIR "$LOCALAPPDATA\Programs\ToolNest-UpdateTest-${KIT_ID}"
  StrCpy $LockHandle 0
  InitPluginsDir
  SetOutPath $PLUGINSDIR
  File /oname=ToolNest-TestLauncher.exe "${PAYLOAD}\ToolNest-TestLauncher.exe"
FunctionEnd
Function ${PREFIX}.onGUIEnd
  ${If} $LockHandle != 0
    System::Call 'kernel32::CloseHandle(p $LockHandle)'
  ${EndIf}
FunctionEnd
!macroend
!insertmacro Init ""
!insertmacro Init "un"
!macro Check
  nsExec::ExecToStack '"$PLUGINSDIR\ToolNest-TestLauncher.exe" --check-maintenance'
  Pop $Result
  Pop $0
  ${If} $Result != 0
    MessageBox MB_ICONSTOP|MB_OK "请先正常退出本组合的 ToolNest（包括托盘），或检查目录归属。不会自动结束应用。$\r$\n$0" /SD IDOK
    Abort
  ${EndIf}
!macroend
!macro Acquire
  !insertmacro Check
  CreateDirectory $INSTDIR
  System::Call 'kernel32::CreateFileW(w "$INSTDIR\.manual-maintenance.lock", i 0xC0000000, i 0, p 0, i 4, i 0x80, p 0) p.r0'
  ${If} $0 == -1
    MessageBox MB_ICONSTOP|MB_OK "另一个测试组合维护操作正在进行，请稍后重试。" /SD IDOK
    Abort
  ${EndIf}
  StrCpy $LockHandle $0
  !insertmacro Check
!macroend
Section
  !insertmacro Acquire
  SetOutPath $INSTDIR
  ClearErrors
  File "${PAYLOAD}\ToolNest.exe"
  File "${PAYLOAD}\ToolNest-TestLauncher.exe"
  File "${PAYLOAD}\kit.json"
  File "${PAYLOAD}\update.pub"
  File "${PAYLOAD}\update.tar.gz"
  File "${PAYLOAD}\update.sig"
  File "${PAYLOAD}\操作步骤.txt"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  ${If} ${Errors}
    MessageBox MB_ICONSTOP|MB_OK "测试文件未能写入，请检查占用后重试。" /SD IDOK
    Abort
  ${EndIf}
  WriteRegStr HKCU "${KEY}" "DisplayName" "ToolNest 更新流程测试 ${KIT_SHORT}"
  WriteRegStr HKCU "${KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${KEY}" "DisplayIcon" '"$INSTDIR\ToolNest.exe",0'
  WriteRegStr HKCU "${KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  CreateDirectory "$SMPROGRAMS\${MENU}"
  CreateShortcut "$SMPROGRAMS\${MENU}\ToolNest 更新测试版.lnk" "$INSTDIR\ToolNest-TestLauncher.exe" "--open" "$INSTDIR\ToolNest.exe" 0
  CreateShortcut "$DESKTOP\ToolNest 更新测试版 ${KIT_SHORT}.lnk" "$INSTDIR\ToolNest-TestLauncher.exe" "--open" "$INSTDIR\ToolNest.exe" 0
  CreateShortcut "$SMPROGRAMS\${MENU}\01 打开隔离测试.lnk" "$INSTDIR\ToolNest-TestLauncher.exe" "--open"
  CreateShortcut "$SMPROGRAMS\${MENU}\02 恢复正常更新源.lnk" "$INSTDIR\ToolNest-TestLauncher.exe" "--set-mode good"
  CreateShortcut "$SMPROGRAMS\${MENU}\03 模拟更新源404.lnk" "$INSTDIR\ToolNest-TestLauncher.exe" "--set-mode source-not-found"
  CreateShortcut "$SMPROGRAMS\${MENU}\04 模拟下载失败503.lnk" "$INSTDIR\ToolNest-TestLauncher.exe" "--set-mode http-error"
  CreateShortcut "$SMPROGRAMS\${MENU}\05 模拟签名校验失败.lnk" "$INSTDIR\ToolNest-TestLauncher.exe" "--set-mode bad-signature"
  CreateShortcut "$SMPROGRAMS\${MENU}\06 停止本地更新服务.lnk" "$INSTDIR\ToolNest-TestLauncher.exe" "--stop"
  CreateShortcut "$SMPROGRAMS\${MENU}\操作步骤.lnk" "$INSTDIR\操作步骤.txt"
  ; 不提供安装后运行，不修改生产卸载登记、真实 PATH、Run 或系统代理。
SectionEnd
Section "Uninstall"
  !insertmacro Acquire
  ClearErrors
  Delete "$INSTDIR\ToolNest.exe"
  Delete "$INSTDIR\ToolNest-TestLauncher.exe"
  ; 更新备份留在程序目录时保留公开归属标记，重装才能验证所有权；不删除任何未知/回退文件。
  Delete "$INSTDIR\update.pub"
  Delete "$INSTDIR\update.tar.gz"
  Delete "$INSTDIR\update.sig"
  Delete "$INSTDIR\操作步骤.txt"
  Delete "$INSTDIR\Uninstall.exe"
  ${If} ${Errors}
    MessageBox MB_ICONSTOP|MB_OK "测试文件仍被占用，卸载登记已保留；请退出测试应用后重试。" /SD IDOK
    Abort
  ${EndIf}
  Delete "$SMPROGRAMS\${MENU}\01 打开隔离测试.lnk"
  Delete "$SMPROGRAMS\${MENU}\ToolNest 更新测试版.lnk"
  Delete "$DESKTOP\ToolNest 更新测试版 ${KIT_SHORT}.lnk"
  Delete "$SMPROGRAMS\${MENU}\02 恢复正常更新源.lnk"
  Delete "$SMPROGRAMS\${MENU}\03 模拟更新源404.lnk"
  Delete "$SMPROGRAMS\${MENU}\04 模拟下载失败503.lnk"
  Delete "$SMPROGRAMS\${MENU}\05 模拟签名校验失败.lnk"
  Delete "$SMPROGRAMS\${MENU}\06 停止本地更新服务.lnk"
  Delete "$SMPROGRAMS\${MENU}\操作步骤.lnk"
  RMDir "$SMPROGRAMS\${MENU}"
  DeleteRegKey HKCU "${KEY}"
  System::Call 'kernel32::CloseHandle(p $LockHandle)'
  StrCpy $LockHandle 0
  Delete "$INSTDIR\.manual-maintenance.lock"
  RMDir "$INSTDIR"
  ; 测试快照、记录与隔离注册表保留供重装核对；绝不递归删除未知内容。
SectionEnd
