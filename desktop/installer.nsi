; The Windows installer of the Electron App (package-win.sh): per user, no admin, into
; %LOCALAPPDATA%\Programs\<name> (where the WebView2 shell, tools/winshell, was installed: installing over it moves
; that one over), Start menu and desktop shortcuts, an entry in Installed apps, and starts it. /S (silent) is how the
; App's own update runs it (internal/desktop platform_windows.go installApp).
;   makensis -DSRC=<packaged dir> -DOUT=<setup.exe> -DICON=<ico> -DVERSION=<v> -DNAME=<name> -DREGKEY=<slug> installer.nsi
Unicode true
SetCompressor /SOLID lzma
RequestExecutionLevel user

!define KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${REGKEY}"

Name "${NAME}"
OutFile "${OUT}"
Icon "${ICON}"
UninstallIcon "${ICON}"
InstallDir "$LOCALAPPDATA\Programs\${NAME}"
InstallDirRegKey HKCU "${KEY}" "InstallLocation"
ShowInstDetails nevershow
AutoCloseWindow true
Page instfiles

Section
  SetOutPath "$INSTDIR"
  ; the WebView2 shell's own files, if it was installed here
  Delete "$INSTDIR\GerdrExp.exe"
  ; the previous Electron version's files: none of them may linger next to the new ones
  RMDir /r "$INSTDIR\resources"
  RMDir /r "$INSTDIR\locales"
  File /r "${SRC}\*"
  WriteUninstaller "$INSTDIR\Uninstall.exe"
  CreateShortCut "$SMPROGRAMS\${NAME}.lnk" "$INSTDIR\${NAME}.exe" "" "$INSTDIR\${NAME}.exe"
  CreateShortCut "$DESKTOP\${NAME}.lnk" "$INSTDIR\${NAME}.exe" "" "$INSTDIR\${NAME}.exe"
  WriteRegStr HKCU "${KEY}" "DisplayName" "${NAME}"
  WriteRegStr HKCU "${KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${KEY}" "Publisher" "Gerdr"
  WriteRegStr HKCU "${KEY}" "DisplayIcon" "$INSTDIR\${NAME}.exe"
  WriteRegStr HKCU "${KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${KEY}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegDWORD HKCU "${KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${KEY}" "NoRepair" 1
SectionEnd

Function .onInstSuccess
  Exec '"$INSTDIR\${NAME}.exe"'
FunctionEnd

Section "Uninstall"
  RMDir /r "$INSTDIR"
  Delete "$SMPROGRAMS\${NAME}.lnk"
  Delete "$DESKTOP\${NAME}.lnk"
  DeleteRegKey HKCU "${KEY}"
SectionEnd
