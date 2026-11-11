; DiskLens by KopiTehnik — Windows installer (Inno Setup 6.3+).
;
; Built by .github/workflows/release.yml:
;   iscc /DAppVersion=0.2.0 /DAppNumVersion=0.2.0.0 /DBinDir=..\..\dist packaging\installer\disklens.iss
; BinDir must contain disklens-windows-amd64.exe and disklens-windows-arm64.exe;
; the installer picks the native one for the machine.
;
; Installs per user by default (no admin rights needed — it must work on a
; locked-down PC and on a nearly full disk), or for all users when the user
; chooses so in the first dialog.

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif
#ifndef AppNumVersion
  #define AppNumVersion "0.0.0.0"
#endif
#ifndef BinDir
  #define BinDir "..\..\dist"
#endif

#define AppName "DiskLens"
#define AppFullName "DiskLens by KopiTehnik"
#define AppPublisher "KopiTehnik"
#define AppURL "https://github.com/ildargiv/disklens"
#define AppExe "disklens.exe"

[Setup]
AppId={{6F1C3E52-8B0A-4C7D-9E21-D15C0B5E7A11}
AppName={#AppFullName}
AppVersion={#AppVersion}
AppVerName={#AppFullName} {#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}/issues
AppUpdatesURL={#AppURL}/releases
VersionInfoVersion={#AppNumVersion}
VersionInfoProductName={#AppFullName}
VersionInfoCompany={#AppPublisher}
VersionInfoDescription={#AppFullName} Setup
DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
ChangesEnvironment=yes
LicenseFile=..\..\LICENSE
SetupIconFile=disklens.ico
UninstallDisplayIcon={app}\{#AppExe}
UninstallDisplayName={#AppFullName}
WizardStyle=modern
Compression=lzma2/ultra64
SolidCompression=yes
OutputDir=..\..\dist
OutputBaseFilename=DiskLens-Setup-{#AppVersion}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"

[CustomMessages]
english.TaskPath=Add disklens to PATH (run it from any terminal)
russian.TaskPath=Добавить disklens в PATH (запуск из любого терминала)
english.TaskContext=Add "Analyze with DiskLens" to the Explorer folder menu
russian.TaskContext=Добавить «Анализировать в DiskLens» в меню папок Проводника
english.ContextVerb=Analyze with DiskLens
russian.ContextVerb=Анализировать в DiskLens
english.RunNow=Open DiskLens for my user folder
russian.RunNow=Открыть DiskLens для папки пользователя
english.GroupIntegration=Integration:
russian.GroupIntegration=Интеграция:

[Tasks]
Name: "addtopath"; Description: "{cm:TaskPath}"; GroupDescription: "{cm:GroupIntegration}"
Name: "contextmenu"; Description: "{cm:TaskContext}"; GroupDescription: "{cm:GroupIntegration}"
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "{#BinDir}\disklens-windows-amd64.exe"; DestDir: "{app}"; DestName: "{#AppExe}"; Check: not IsArm64; Flags: ignoreversion
Source: "{#BinDir}\disklens-windows-arm64.exe"; DestDir: "{app}"; DestName: "{#AppExe}"; Check: IsArm64; Flags: ignoreversion
Source: "..\..\LICENSE"; DestDir: "{app}"; DestName: "LICENSE.txt"; Flags: ignoreversion
Source: "..\..\README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "disklens.ico"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\{#AppName}"; Filename: "{app}\{#AppExe}"; Parameters: """{%USERPROFILE}"""; WorkingDir: "{%USERPROFILE}"; IconFilename: "{app}\disklens.ico"; Comment: "{#AppFullName}"
Name: "{autodesktop}\{#AppName}"; Filename: "{app}\{#AppExe}"; Parameters: """{%USERPROFILE}"""; WorkingDir: "{%USERPROFILE}"; IconFilename: "{app}\disklens.ico"; Tasks: desktopicon

[Registry]
; Right-click a folder, the background of an open folder, or a drive.
Root: HKA; Subkey: "Software\Classes\Directory\shell\DiskLens"; ValueType: string; ValueName: ""; ValueData: "{cm:ContextVerb}"; Tasks: contextmenu; Flags: uninsdeletekey
Root: HKA; Subkey: "Software\Classes\Directory\shell\DiskLens"; ValueType: string; ValueName: "Icon"; ValueData: "{app}\disklens.ico"; Tasks: contextmenu
Root: HKA; Subkey: "Software\Classes\Directory\shell\DiskLens\command"; ValueType: string; ValueName: ""; ValueData: """{app}\{#AppExe}"" ""%V"""; Tasks: contextmenu
Root: HKA; Subkey: "Software\Classes\Directory\Background\shell\DiskLens"; ValueType: string; ValueName: ""; ValueData: "{cm:ContextVerb}"; Tasks: contextmenu; Flags: uninsdeletekey
Root: HKA; Subkey: "Software\Classes\Directory\Background\shell\DiskLens"; ValueType: string; ValueName: "Icon"; ValueData: "{app}\disklens.ico"; Tasks: contextmenu
Root: HKA; Subkey: "Software\Classes\Directory\Background\shell\DiskLens\command"; ValueType: string; ValueName: ""; ValueData: """{app}\{#AppExe}"" ""%V"""; Tasks: contextmenu
Root: HKA; Subkey: "Software\Classes\Drive\shell\DiskLens"; ValueType: string; ValueName: ""; ValueData: "{cm:ContextVerb}"; Tasks: contextmenu; Flags: uninsdeletekey
Root: HKA; Subkey: "Software\Classes\Drive\shell\DiskLens"; ValueType: string; ValueName: "Icon"; ValueData: "{app}\disklens.ico"; Tasks: contextmenu
Root: HKA; Subkey: "Software\Classes\Drive\shell\DiskLens\command"; ValueType: string; ValueName: ""; ValueData: """{app}\{#AppExe}"" ""%V"""; Tasks: contextmenu

[Run]
Filename: "{app}\{#AppExe}"; Parameters: """{%USERPROFILE}"""; WorkingDir: "{%USERPROFILE}"; Description: "{cm:RunNow}"; Flags: postinstall nowait skipifsilent unchecked

[Code]
const
  EnvKeyUser = 'Environment';
  EnvKeyMachine = 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment';

function EnvRoot: Integer;
begin
  if IsAdminInstallMode then
    Result := HKEY_LOCAL_MACHINE
  else
    Result := HKEY_CURRENT_USER;
end;

function EnvKey: String;
begin
  if IsAdminInstallMode then
    Result := EnvKeyMachine
  else
    Result := EnvKeyUser;
end;

procedure AddToPath(Dir: String);
var
  Paths: String;
begin
  if not RegQueryStringValue(EnvRoot, EnvKey, 'Path', Paths) then
    Paths := '';
  if Pos(';' + Uppercase(Dir) + ';', ';' + Uppercase(Paths) + ';') > 0 then
    exit;
  if (Paths <> '') and (Copy(Paths, Length(Paths), 1) <> ';') then
    Paths := Paths + ';';
  RegWriteExpandStringValue(EnvRoot, EnvKey, 'Path', Paths + Dir);
end;

procedure RemoveFromPath(Dir: String);
var
  Paths, S: String;
  P: Integer;
begin
  if not RegQueryStringValue(EnvRoot, EnvKey, 'Path', Paths) then
    exit;
  S := ';' + Paths + ';';
  P := Pos(';' + Uppercase(Dir) + ';', Uppercase(S));
  if P = 0 then
    exit;
  Delete(S, P, Length(Dir) + 1);
  RegWriteExpandStringValue(EnvRoot, EnvKey, 'Path', Copy(S, 2, Length(S) - 2));
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if (CurStep = ssPostInstall) and WizardIsTaskSelected('addtopath') then
    AddToPath(ExpandConstant('{app}'));
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
    RemoveFromPath(ExpandConstant('{app}'));
end;
