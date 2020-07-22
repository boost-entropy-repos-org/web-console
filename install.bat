@echo off
echo Installing...

:paramLoop
if "%1"=="" goto paramContinue
if "%1"=="--key" (
  shift
  set key=%1
)
if "%1"=="--subdomain" (
  shift
  set subdomain=%1
)
shift
goto paramLoop
:paramContinue

rem Place the executables in the appropriate folder.
erase "C:\Program Files\WebConsole\webconsole.exe" > nul 2>&1
copy webconsole.exe "C:\Program Files\WebConsole" > nul 2>&1
copy tunnelto\tunnelto.exe "C:\Program Files\WebConsole"
mkdir "C:\Program Files\WebConsole\www" > nul 2>&1
mkdir "C:\Program Files\WebConsole\tasks" > nul 2>&1
xcopy /E /Y www "C:\Program Files\WebConsole\www" > nul 2>&1

rem Set up the WebConsole service.
net stop WebConsole
nssm-2.24\win64\nssm install WebConsole "C:\Program Files\WebConsole\webconsole.exe"
nssm-2.24\win64\nssm set WebConsole DisplayName "Web Console"
nssm-2.24\win64\nssm set WebConsole AppNoConsole 1
nssm-2.24\win64\nssm set WebConsole Start SERVICE_AUTO_START
net start WebConsole

rem Allow the WbConsole service through the (local) Windows firewall.
netsh.exe advfirewall firewall add rule name="WebConsole" program="C:\Program Files\WebConsole\webconsole.exe" protocol=tcp dir=in enable=yes action=allow profile="private,domain,public"

echo Key:
echo %key%
echo subdomain
echo %subdomain%
rem Set up the TunnelTo.dev service.
net stop TunnelTo
nssm-2.24\win64\nssm install TunnelTo "C:\Program Files\WebConsole\tunnelto.exe" --port 8090 --key %key% --subdomain %subdomain%
nssm-2.24\win64\nssm set TunnelTo DisplayName "TunnelTo.dev"
nssm-2.24\win64\nssm set TunnelTo AppNoConsole 1
nssm-2.24\win64\nssm set TunnelTo Start SERVICE_AUTO_START
net start TunnelTo
