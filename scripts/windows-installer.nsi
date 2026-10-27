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

!define SILVERWING_INSTALLER_MUTEX "Global\SilverwingProduct.Setup.A3D96A91-A716-5D1F-9C9C-885B8ECFB731"
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
      MessageBox MB_OK|MB_ICONSTOP "另一个 silverwing-product 安装或卸载程序正在运行，请等待完成后重试。"
    Quit
  ${EndIf}
!macroend

Name "silverwing-product ${PRODUCT_VERSION}"
Caption "silverwing-product ${PRODUCT_VERSION} Setup"
OutFile "${OUTPUT_FILE}"
InstallDir "$LOCALAPPDATA\Programs\silverwing-product"
InstallDirRegKey HKCU "Software\SilverwingProduct" "InstallDir"
ShowInstDetails show
ShowUnInstDetails show

Page directory DirectoryPagePre
Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

Function .onInit
  !insertmacro AcquireInstallerMutex
  ${IfNot} ${RunningX64}
    MessageBox MB_OK|MB_ICONSTOP "silverwing-product 需要 64 位 Windows。"
    Abort
  ${EndIf}
  SetRegView 64
  ReadRegDWORD $0 HKCU "Software\SilverwingProduct\Installer" "installed"
  StrCmp $0 1 0 +3
    MessageBox MB_OK|MB_ICONSTOP "请先卸载 MSI 版本的 silverwing-product，再使用 Setup 安装。"
    Abort
  ReadRegStr $ExistingInstallDir HKCU "Software\SilverwingProduct" "InstallDir"
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
  ReadRegDWORD $0 HKCU "Software\SilverwingProduct\Installer" "installed"
  StrCmp $0 1 0 +2
    Abort "请先卸载 MSI 版本的 silverwing-product。"
  ReadRegStr $ExistingInstallDir HKCU "Software\SilverwingProduct" "InstallDir"
  ${If} $ExistingInstallDir != ""
    StrCpy $INSTDIR $ExistingInstallDir
  ${EndIf}
  SetOutPath "$INSTDIR"
  File /r "${STAGING_DIR}\*"

  CreateDirectory "$SMPROGRAMS\silverwing-product"
  CreateShortCut "$SMPROGRAMS\silverwing-product\silverwing-product.lnk" "$INSTDIR\silverwing-product.exe" "" "$INSTDIR\silverwing-product.exe"
  CreateShortCut "$DESKTOP\silverwing-product.lnk" "$INSTDIR\silverwing-product.exe" "" "$INSTDIR\silverwing-product.exe"

  WriteRegStr HKCU "Software\SilverwingProduct" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\SilverwingProduct" "DisplayName" "silverwing-product ${PRODUCT_VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\SilverwingProduct" "DisplayVersion" "${PRODUCT_VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\SilverwingProduct" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteUninstaller "$INSTDIR\Uninstall.exe"
SectionEnd

Section "Uninstall"
  StrCpy $UninstallFailed 0
  !include "${UNINSTALL_MANIFEST}"
  StrCmp $UninstallFailed 0 +3
    MessageBox MB_OK|MB_ICONSTOP "部分程序文件无法移除。请关闭 silverwing-product 后重新卸载，卸载入口已保留。"
    Abort
  ; An older copy must not remove the current installation's shared entries.
  ReadRegStr $0 HKCU "Software\SilverwingProduct" "InstallDir"
  ${If} $0 == $INSTDIR
    Delete "$DESKTOP\silverwing-product.lnk"
    Delete "$SMPROGRAMS\silverwing-product\silverwing-product.lnk"
    RMDir "$SMPROGRAMS\silverwing-product"
    DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\SilverwingProduct"
    DeleteRegValue HKCU "Software\SilverwingProduct" "InstallDir"
    DeleteRegKey /ifempty HKCU "Software\SilverwingProduct"
  ${EndIf}
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
SectionEnd
