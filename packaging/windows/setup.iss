; setup.iss — the Windows installer (Inno Setup), build-plan step 11.
;
; What it does, in order, with no terminal at any point (CLAUDE.md Rule 1):
;   1. Copies advisor.exe into Program Files.
;   2. Adds a Start Menu shortcut and an uninstaller entry.
;   3. Offers an optional desktop shortcut (unchecked by default).
;   4. Offers an optional "start at login" checkbox (unchecked by default,
;      matching every other place this app asks before it acts) that
;      writes to the *same* HKCU Run-key value internal/autostart's tray
;      toggle uses (windowsRunValueName = "LocalLLMAdvisor",
;      internal/autostart/windows_logic.go) — one source of truth, whether
;      the user turned it on here or later from the tray menu.
;   5. Offers to launch the app when the wizard finishes.
;
; It does NOT write anything about the AppUserModelID: the app registers
; its own AUMID (winapp.AUMID, "ItayPollak.LocalLLMAdvisor") at every
; process start via SetCurrentProcessExplicitAppUserModelID, which is all
; Windows 10 1903+ needs for toast notifications from an unpackaged EXE —
; see internal/winapp/winapp.go's doc comment. Nothing installer-side is
; needed for that.
;
; Compile with Inno Setup 6 (`iscc`), passing the version and the path to
; the already-built windows/amd64 binary:
;
;   iscc /DMyAppVersion=0.3.1 /DSourceExePath=..\..\dist\advisor-windows-amd64.exe setup.iss
;
; Unsigned (the default until Itay buys a code-signing certificate —
; RELEASING.md has the secrets table): that's the whole command above.
; Windows SmartScreen will warn on first run; INSTALL.md says so plainly
; and shows the click-through.
;
; Signed, once a certificate secret exists in CI: add /DSIGN and tell Inno
; Setup what "signtool" means via the /S command-line switch (never
; hardcoded here, so this file never needs to change when signing turns
; on) —
;
;   iscc /DSIGN /DMyAppVersion=0.3.1 /DSourceExePath=..\..\dist\advisor-windows-amd64.exe ^
;        /Ssigntool="signtool.exe sign /sha1 $qWINDOWS_CERT_THUMBPRINT$q /fd SHA256 /tr http://timestamp.digicert.com /td SHA256 $f" ^
;        setup.iss
;
; AppId is a fixed GUID — like internal/autostart's per-OS identifiers, it
; must NEVER change once shipped (RELEASING.md): it is how Windows'
; "Programs and Features" and the installer itself recognize "this is an
; upgrade of the same app" rather than a second, separate install.

#ifndef MyAppVersion
  #define MyAppVersion "0.0.0"
#endif
#ifndef SourceExePath
  #define SourceExePath "..\..\dist\advisor-windows-amd64.exe"
#endif

#define MyAppName "Local LLM Advisor"
#define MyAppPublisher "Itay Pollak"
#define MyAppURL "https://github.com/itayp/-Local-LLM-Advisor-n-Optimizer"
#define MyAppExeName "advisor.exe"

; internal/autostart's HKCU Run-key value name (windowsRunValueName) —
; kept in sync by hand since Inno Setup scripts can't import a Go
; constant; internal/autostart/windows_logic_test.go pins the Go side.
#define AutostartValueName "LocalLLMAdvisor"

[Setup]
AppId={{EE047489-1E0E-4A31-A1F7-2E7F1A5A4132}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
; No VersionInfoVersion: it demands a strict "a.b.c.d" numeric format, and
; MyAppVersion is `git describe` output (e.g. "v0.3.1-2-gabc1234-dirty")
; more often than a bare release tag — AppVersion above (a free-form
; display string, what "Programs and Features" actually shows) already
; carries it honestly without that constraint.
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
UninstallDisplayIcon={app}\{#MyAppExeName}
OutputDir=..\..\dist
OutputBaseFilename=LocalLLMAdvisor-Setup-{#MyAppVersion}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64
ArchitecturesInstallIn64BitMode=x64
; Program Files needs an elevated install; the Run-key and AUMID work this
; installer and the app do afterward are HKCU-only and need no further
; elevation (internal/autostart, internal/winapp).
PrivilegesRequired=admin
#ifdef SIGN
SignTool=signtool
#endif

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a &desktop shortcut"; GroupDescription: "Additional shortcuts:"; Flags: unchecked
Name: "runatlogin"; Description: "&Start Local LLM Advisor automatically when I log in"; GroupDescription: "Startup:"; Flags: unchecked

[Files]
Source: "{#SourceExePath}"; DestDir: "{app}"; DestName: "{#MyAppExeName}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"
Name: "{group}\Uninstall {#MyAppName}"; Filename: "{uninstallexe}"
Name: "{autodesktop}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon

[Registry]
; The exact same value internal/autostart/autostart_windows.go's tray
; toggle reads and writes — checking this box here or turning "Start at
; login" on later from the tray menu land on one shared piece of state,
; never two. ValueData quotes the exe path the way CreateProcess's command
; line parser expects (internal/autostart/windows_logic.go's
; quoteWindowsArg does the same thing on the Go side).
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "{#AutostartValueName}"; ValueData: """{app}\{#MyAppExeName}"" -tray"; Tasks: runatlogin; Flags: uninsdeletevalue

[Run]
; -tray, not a bare launch: the installed app is meant to live in the tray,
; the same as the Start Menu shortcut and the Run-key entry above. First
; launch ever opens the browser once (cmd/advisor's own
; app.launched_before gate) — nothing more the installer needs to do.
Filename: "{app}\{#MyAppExeName}"; Parameters: "-tray"; Description: "Launch {#MyAppName}"; Flags: postinstall nowait skipifsilent

[UninstallDelete]
; The uninstaller removes the program files and shortcuts (Inno's
; default) but deliberately leaves the user's own data —
; %LOCALAPPDATA%\Advisor (internal/store.DefaultDataDir on Windows) —
; untouched: models the user picked, settings, benchmark history. Not
; listed here on purpose; INSTALL.md says so, so nobody is surprised
; either way.
