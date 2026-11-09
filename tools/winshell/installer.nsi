; The Windows installer of the Windows shell (package.sh runs makensis with SRC, OUT, ICON, VERSION, NAME, REGKEY).
; Per user, no admin rights (the Electron build's nsis: oneClick, perMachine false): %LOCALAPPDATA%\Programs.
Unicode true
SetCompressor /SOLID lzma
RequestExecutionLevel user

;!define NAME: from package.sh (Gerdr / Gerdr exp)
!define KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${REGKEY}"

Name "${NAME}"
OutFile "${OUT}"
Icon "${ICON}"
UninstallIcon "${ICON}"
InstallDir "$LOCALAPPDATA\Programs\${NAME}"
; an update (the shell runs the new installer with /S) goes where the installed one is
InstallDirRegKey HKCU "${KEY}" "InstallLocation"
ShowInstDetails nevershow
AutoCloseWindow true
Page instfiles

Section
  SetOutPath "$INSTDIR"
  File "${SRC}\GerdrExp.exe"
  File "${SRC}\gerdr.exe"
  File "${SRC}\icon.ico"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  CreateShortCut "$SMPROGRAMS\${NAME}.lnk" "$INSTDIR\GerdrExp.exe" "" "$INSTDIR\icon.ico"
  CreateShortCut "$DESKTOP\${NAME}.lnk" "$INSTDIR\GerdrExp.exe" "" "$INSTDIR\icon.ico"
  WriteRegStr HKCU "${KEY}" "DisplayName" "${NAME}"
  WriteRegStr HKCU "${KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${KEY}" "Publisher" "Gerdr"
  WriteRegStr HKCU "${KEY}" "DisplayIcon" "$INSTDIR\icon.ico"
  WriteRegStr HKCU "${KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegDWORD HKCU "${KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${KEY}" "NoRepair" 1
SectionEnd

Function .onInstSuccess
  Exec '"$INSTDIR\GerdrExp.exe"'
FunctionEnd

Section "Uninstall"
  Delete "$INSTDIR\GerdrExp.exe"
  Delete "$INSTDIR\gerdr.exe"
  Delete "$INSTDIR\icon.ico"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
  Delete "$SMPROGRAMS\${NAME}.lnk"
  Delete "$DESKTOP\${NAME}.lnk"
  DeleteRegKey HKCU "${KEY}"
  ; %LOCALAPPDATA%\Gerdr exp (WebView2 profile, log) and ~\.gerdr (sessions, settings) stay: the data is the user's.
SectionEnd
