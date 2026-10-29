Unicode true
ManifestDPIAware true
RequestExecutionLevel user
!include "LogicLib.nsh"
!include "x64.nsh"

!ifndef PRODUCT_VERSION
  !error "PRODUCT_VERSION is required"
!endif
!ifndef STAGING_DIR
  !error "STAGING_DIR is required"
!endif
!ifndef OUTPUT_FILE
  !error "OUTPUT_FILE is required"
!endif
!ifndef UNINSTALL_MANIFEST
  !error "UNINSTALL_MANIFEST is required"
!endif
Var UninstallFailed
Var ExistingInstallDir
Var InstallerMutex

!define SILVERWING_INSTALLER_MUTEX "${PROFILE_WindowsInstallerMutex}"
!macro AcquireInstallerMutex
  ; Retain the handle until process exit, before any installation state is read.
  System::Call 'kernel32::CreateMutexW(p 0, i 0, w "${SILVERWING_INSTALLER_MUTEX}") p.r1 ?e'
  Pop $0
  StrCpy $InstallerMutex $1
  ${If} $InstallerMutex == 0
    SetErrorLevel 1603
    IfSilent +2
      MessageBox MB_OK|MB_ICONSTOP "无法锁定安装状态，请稍后重试。"
    Quit
  ${EndIf}
  ${If} $0 == 183
    SetErrorLevel 1618
    IfSilent +2
      MessageBox MB_OK|MB_ICONSTOP "另一个 ${PROFILE_WindowsInstallName} 安装或卸载程序正在运行，请等待完成后重试。"
    Quit
  ${EndIf}
!macroend

Name "${PROFILE_Name} ${PRODUCT_VERSION}"
Caption "${PROFILE_Name} ${PRODUCT_VERSION} Setup"
OutFile "${OUTPUT_FILE}"
InstallDir "$LOCALAPPDATA\Programs\${PROFILE_WindowsInstallName}"
InstallDirRegKey HKCU "Software\${PROFILE_WindowsRegistryNamespace}" "InstallDir"
ShowInstDetails show
ShowUnInstDetails show

Page directory DirectoryPagePre
Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

Function .onInit
  !insertmacro AcquireInstallerMutex
  ${IfNot} ${RunningX64}
    MessageBox MB_OK|MB_ICONSTOP "${PROFILE_WindowsInstallName} 需要 64 位 Windows。"
    Abort
  ${EndIf}
  SetRegView 64
  ReadRegDWORD $0 HKCU "Software\${PROFILE_WindowsRegistryNamespace}\Installer" "installed"
  StrCmp $0 1 0 +3
    MessageBox MB_OK|MB_ICONSTOP "请先卸载 MSI 版本的 ${PROFILE_WindowsInstallName}，再使用 Setup 安装。"
    Abort
  ReadRegStr $ExistingInstallDir HKCU "Software\${PROFILE_WindowsRegistryNamespace}" "InstallDir"
  ${If} $ExistingInstallDir != ""
    StrCpy $INSTDIR $ExistingInstallDir
  ${EndIf}
FunctionEnd

Function DirectoryPagePre
  ; Reinstallation updates the registered copy instead of leaving a second one.
  ${If} $ExistingInstallDir != ""
    Abort
  ${EndIf}
FunctionEnd

Function un.onInit
  !insertmacro AcquireInstallerMutex
  SetRegView 64
FunctionEnd

Section "Silverwing"
  ; Recheck in the execution section, including silent installation.
  ReadRegDWORD $0 HKCU "Software\${PROFILE_WindowsRegistryNamespace}\Installer" "installed"
  StrCmp $0 1 0 +2
    Abort "请先卸载 MSI 版本的 ${PROFILE_WindowsInstallName}。"
  ReadRegStr $ExistingInstallDir HKCU "Software\${PROFILE_WindowsRegistryNamespace}" "InstallDir"
  ${If} $ExistingInstallDir != ""
    StrCpy $INSTDIR $ExistingInstallDir
  ${EndIf}
  SetOutPath "$INSTDIR"
  File /r "${STAGING_DIR}\*"

  CreateDirectory "$SMPROGRAMS\${PROFILE_WindowsInstallName}"
  CreateShortCut "$SMPROGRAMS\${PROFILE_WindowsInstallName}\${PROFILE_WindowsInstallName}.lnk" "$INSTDIR\${PROFILE_ExecutableName}.exe" "" "$INSTDIR\${PROFILE_ExecutableName}.exe"
  CreateShortCut "$DESKTOP\${PROFILE_WindowsInstallName}.lnk" "$INSTDIR\${PROFILE_ExecutableName}.exe" "" "$INSTDIR\${PROFILE_ExecutableName}.exe"

  WriteRegStr HKCU "Software\${PROFILE_WindowsRegistryNamespace}" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PROFILE_WindowsRegistryNamespace}" "DisplayName" "${PROFILE_WindowsInstallName} ${PRODUCT_VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PROFILE_WindowsRegistryNamespace}" "DisplayVersion" "${PRODUCT_VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PROFILE_WindowsRegistryNamespace}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteUninstaller "$INSTDIR\Uninstall.exe"
SectionEnd

Section "Uninstall"
  StrCpy $UninstallFailed 0
  !include "${UNINSTALL_MANIFEST}"
  StrCmp $UninstallFailed 0 +3
    MessageBox MB_OK|MB_ICONSTOP "部分程序文件无法移除。请关闭 ${PROFILE_WindowsInstallName} 后重新卸载，卸载入口已保留。"
    Abort
  ; An older copy must not remove the current installation's shared entries.
  ReadRegStr $0 HKCU "Software\${PROFILE_WindowsRegistryNamespace}" "InstallDir"
  ${If} $0 == $INSTDIR
    Delete "$DESKTOP\${PROFILE_WindowsInstallName}.lnk"
    Delete "$SMPROGRAMS\${PROFILE_WindowsInstallName}\${PROFILE_WindowsInstallName}.lnk"
    RMDir "$SMPROGRAMS\${PROFILE_WindowsInstallName}"
    DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PROFILE_WindowsRegistryNamespace}"
    DeleteRegValue HKCU "Software\${PROFILE_WindowsRegistryNamespace}" "InstallDir"
    DeleteRegKey /ifempty HKCU "Software\${PROFILE_WindowsRegistryNamespace}"
  ${EndIf}
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
SectionEnd
