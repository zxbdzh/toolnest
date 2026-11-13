#requires -Version 5.1
[CmdletBinding()]
param([switch]$Probe)
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
$InputRoot='C:\ToolNestAcceptance\input'
$Evidence='C:\ToolNestAcceptance\evidence'
$Work='C:\ToolNestAcceptance\work'
if ($PSScriptRoot -cne $InputRoot -or $env:USERNAME -ne 'WDAGUtilityAccount') { throw 'Guest script is restricted to the mapped Windows Sandbox account and directory.' }
$account=[Security.Principal.WindowsIdentity]::GetCurrent().Name.Split('\')[-1]
if($account -ine 'WDAGUtilityAccount'){throw 'Guest script is restricted to the mapped Windows Sandbox actual token account.'}
[void][IO.Directory]::CreateDirectory($Work)
$Results=[Collections.Generic.List[object]]::new()
function Record([string]$Name,[string]$Status,[string]$Detail) { $Results.Add([ordered]@{name=$Name;status=$Status;detail=$Detail}) }
function Json([string]$Name,$Value) { $Value | ConvertTo-Json -Depth 15 | Set-Content -LiteralPath (Join-Path $Evidence $Name) -Encoding UTF8 }
function Check([bool]$Condition,[string]$Message) { if (!$Condition) { throw $Message } }
function FileTree([string]$Directory) {
    # 固定应用数据根，限深两层；不会递归扫描 Sandbox 或宿主盘。
    $state=@{}
    if (Test-Path -LiteralPath $Directory) {
        $files=@(Get-ChildItem -LiteralPath $Directory -File)
        foreach($child in @(Get-ChildItem -LiteralPath $Directory -Directory)) { $files+=@(Get-ChildItem -LiteralPath $child.FullName -File) }
        foreach($file in $files) { $state[$file.FullName.Substring($Directory.Length)]=(Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash }
    }
    return $state
}
function Run([string]$File,[string[]]$Arguments,[string]$Name) {
    $stdout=Join-Path $Evidence ($Name+'.stdout.txt'); $stderr=Join-Path $Evidence ($Name+'.stderr.txt')
    $process=Start-Process -FilePath $File -ArgumentList $Arguments -PassThru -WindowStyle Hidden -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    if (!$process.WaitForExit(180000)) { throw "Bounded guest command timed out, pid=$($process.Id), step=$Name; no forced termination" }
    if ($process.ExitCode -ne 0) { throw "Guest step $Name exited $($process.ExitCode)" }
}
function Wait-Guest([scriptblock]$Condition,[int]$Seconds,[string]$Description) {
    $deadline=[DateTime]::UtcNow.AddSeconds($Seconds)
    do{if(& $Condition){return};Start-Sleep -Milliseconds 250}while([DateTime]::UtcNow -lt $deadline)
    throw "BLOCKED: timeout (${Seconds}s): $Description. Complete this step manually inside the authorized Sandbox; do not alter state files."
}
function Read-State([string]$Path){if(Test-Path -LiteralPath $Path){try{return (Get-Content -LiteralPath $Path -Raw|ConvertFrom-Json)}catch{return $null}};return $null}
function UI([int]$Owner,[string]$Action,[string]$Name='', [string]$Value='') {
    try{& (Join-Path $InputRoot 'Window-Automation.ps1') -ProcessId $Owner -Output (Join-Path $Evidence ('at18-'+$Action+'.png')) -Action $Action -ControlName $Name -Value $Value -NoCapture}
    catch{throw "UIA_BLOCKED: $Action / $Name. $($_.Exception.Message)"}
}
function Wait-UI([int]$Owner,[string]$Pattern,[int]$Seconds=20){
    Wait-Guest { $view=UI $Owner 'Inspect'; @($view.automation.controls|Where-Object {$_.name -match $Pattern}).Count -gt 0 } $Seconds "UI status: $Pattern"
}
function Start-GuestWindow([string]$Application,[string[]]$Arguments){
    $process=Start-Process -FilePath $Application -ArgumentList $Arguments -PassThru
    Wait-Guest {$process.Refresh();if($process.HasExited){throw 'Guest application exited before its window was ready.'};$process.MainWindowHandle -ne 0} 20 'installed application window'
    return $process
}
function Exit-GuestWindow($Process){
    $Process.Refresh();if($Process.HasExited){return}
    [void]$Process.CloseMainWindow()
    Wait-UI $Process.Id '^退出应用，停止应用内计划$'
    $null=UI $Process.Id 'Select' '退出应用，停止应用内计划'
    $null=UI $Process.Id 'Invoke' '确认关闭'
    Wait-Guest {$Process.Refresh();$Process.HasExited} 30 'normal UI exit (never force-kill a critical write)'
}
function Isolated-Processes([string]$Identifier){
    # 只读本轮隔离标识对应的 ToolNest；包含私有 helper，维护安装前必须全部退出。
    @(Get-CimInstance Win32_Process -Filter "Name='ToolNest.exe'"|Where-Object {$_.CommandLine -and $_.CommandLine.Contains($Identifier)})
}
function Run-InstalledUpdate([string]$Application,[string]$Data,[string]$Identifier){
    $active=$null;$restartRequested=$false;$confirmed=$false;$safe=$true
    $settingsFile=Join-Path $Data 'settings.json';$preparedFile=Join-Path $Data 'updates\prepared.json';$switchFile=Join-Path $Data 'updates\switch.json'
    $arguments=@('--data-dir',$Data,'--test-root',$Identifier)
    try{
        foreach($name in @('update-0.1.1.tar.gz','update-0.1.1.sig','update.pub','acceptance-runner.exe')){
            if(!(Test-Path -LiteralPath (Join-Path $InputRoot $name))){throw "BLOCKED: missing signed test input: $name"}
        }
        # 先由真实 UI 关闭自动检查/下载，避免十秒后台检查抢在代理保存前准备更新。
        $active=Start-GuestWindow $Application $arguments
        Wait-UI $active.Id '^稍后再说$'
        $null=UI $active.Id 'Invoke' '稍后再说'
        Wait-Guest {$state=Read-State $settingsFile;$null -ne $state -and $state.settings.OnboardingDone} 20 'onboarding skip persisted'
        $null=UI $active.Id 'Invoke' '应用设置'
        $null=UI $active.Id 'Toggle' '自动检查更新' 'Off'
        $null=UI $active.Id 'Toggle' '自动下载更新' 'Off'
        $null=UI $active.Id 'Invoke' '保存设置'
        Wait-Guest {$state=Read-State $settingsFile;$null -ne $state -and !$state.settings.AutoCheck -and !$state.settings.AutoDownload} 20 'UI-disabled background updates persisted'
        Exit-GuestWindow $active;$active=$null
        $private=Join-Path $Work ('fixture-'+$Identifier)
        $fixture=Start-Process -FilePath (Join-Path $InputRoot 'acceptance-runner.exe') -ArgumentList @('--out',$private,'--archive',(Join-Path $InputRoot 'update-0.1.1.tar.gz'),'--signature-file',(Join-Path $InputRoot 'update-0.1.1.sig'),'--public-key-file',(Join-Path $InputRoot 'update.pub'),'--duration','5m') -PassThru -WindowStyle Hidden -RedirectStandardOutput (Join-Path $Evidence 'at18-fixture.json') -RedirectStandardError (Join-Path $Evidence 'at18-fixture.stderr.txt')
        $ready=Join-Path $private 'fixture-private.json'
        Wait-Guest {$fixture.Refresh();if($fixture.HasExited){throw 'Signed loopback fixture exited before readiness.'};Test-Path -LiteralPath $ready} 10 'signed loopback fixture readiness'
        $configuration=Read-State $ready
        foreach($endpoint in @($configuration.feed,$configuration.httpProxy,$configuration.socksProxy)){
            $uri=[uri]$endpoint
            Check ($uri.Host -ceq '127.0.0.1' -and $uri.Port -gt 0 -and !$uri.UserInfo) 'Fixture must use numeric loopback without embedded credentials.'
        }
        Check ($configuration.publicKey -ceq (Get-Content -LiteralPath (Join-Path $InputRoot 'update.pub') -Raw).Trim()) 'Fixture public key differs from supplied signed input.'
        $arguments+=@('--test-update-source',$configuration.feed,'--test-update-key-file',(Join-Path $InputRoot 'update.pub'))
        $oldHash=(Get-FileHash -LiteralPath $Application -Algorithm SHA256).Hash
        $active=Start-GuestWindow $Application $arguments
        $oldId=$active.Id
        $null=UI $oldId 'Select' '自动备份'
        $null=UI $oldId 'Toggle' '启用自动备份' 'On'
        $null=UI $oldId 'Invoke' '保存计划'
        Wait-Guest {$state=Read-State $settingsFile;$null -ne $state -and $state.settings.ScheduleEnabled -and $state.schedule.enabled -and [DateTimeOffset]::Parse($state.schedule.next).Year -gt 2000} 20 'enabled schedule and next run persisted'
        $snapshots=Join-Path $Data 'snapshots'
        $beforeCount=@(Get-ChildItem -LiteralPath $snapshots -Filter '*.json' -File -ErrorAction SilentlyContinue).Count
        $null=UI $oldId 'Invoke' '立即备份'
        Wait-UI $oldId '^手动备份完成$'
        Wait-Guest {@(Get-ChildItem -LiteralPath $snapshots -Filter '*.json' -File -ErrorAction SilentlyContinue).Count -gt $beforeCount} 20 'UI-created snapshot'
        $snapshotBefore=FileTree $snapshots
        Check ($snapshotBefore.Count -gt 0) 'No original snapshots exist.'
        $null=UI $oldId 'Invoke' '应用设置'
        $null=UI $oldId 'Select' '自定义'
        $null=UI $oldId 'SetValue' '代理地址' $configuration.httpProxy
        $null=UI $oldId 'SetValue' '代理账号' $configuration.username
        $null=UI $oldId 'SetValue' '代理密码' $configuration.credential
        $null=UI $oldId 'Invoke' '保存设置'
        Wait-Guest {$state=Read-State $settingsFile;$null -ne $state -and $state.settings.ProxyMode -eq 'custom' -and $state.settings.ProxyURL -ceq $configuration.httpProxy -and $state.credential_id} 20 'authenticated HTTP proxy saved by UI'
        $null=UI $oldId 'Invoke' '使用草稿测试连接'
        Wait-UI $oldId '^更新入口连接正常' 35
        Check (!(Test-Path -LiteralPath $preparedFile)) 'Update was staged before the explicit authenticated proxy check.'
        $null=UI $oldId 'Invoke' '检查更新'
        Wait-UI $oldId '^发现新版本$' 35
        $null=UI $oldId 'Invoke' '版本说明'
        Wait-UI $oldId '^版本说明 · 0\.1\.1$'
        $null=UI $oldId 'Invoke' '关闭详情'
        $null=UI $oldId 'Invoke' '下载更新'
        Wait-UI $oldId '^更新已就绪$' 75
        $prepared=Read-State $preparedFile
        Check ($null -ne $prepared -and $prepared.version -eq '0.1.1') 'UI ready lacks actual signed staging evidence.'
        Check ((Get-FileHash -LiteralPath $Application -Algorithm SHA256).Hash -ceq $oldHash) 'Ready update changed the old executable without user consent.'
        $active.Refresh();Check (!$active.HasExited) 'Old version restarted automatically before consent.'
        # 稍后更新必须继续旧进程；Hide 不是退出，也不得绕过重启确认。
        $null=UI $oldId 'Invoke' '重启更新…'
        $null=UI $oldId 'Invoke' '稍后更新'
        Start-Sleep -Milliseconds 500
        $active.Refresh();Check (!$active.HasExited -and (Get-FileHash -LiteralPath $Application).Hash -ceq $oldHash) 'Deferring update did not preserve the running old version.'
        $stateBefore=Read-State $settingsFile
        Json 'at18-before.json' @{version='0.1.0';pid=$oldId;executableSHA256=$oldHash;snapshotHashes=$snapshotBefore;schedule=$stateBefore.schedule;proxyProtocol='http';credentialsExported=$false}
        $null=UI $oldId 'Invoke' '重启更新…'
        $restartRequested=$true
        $null=UI $oldId 'Invoke' '重启更新'
        Wait-Guest {$active.Refresh();$active.HasExited} 45 'old installed process exits after user restart confirmation'
        Wait-Guest {$journal=Read-State $switchFile;$null -ne $journal -and $journal.phase -eq 'confirmed'} 65 'new first-frame startup confirmation (switch.json)'
        $journal=Read-State $switchFile
        Check ($journal.version -eq '0.1.1' -and $journal.previousVersion -eq '0.1.0' -and $journal.target -ieq $Application) 'Confirmed switch did not target the installed 0.1.0 -> 0.1.1 executable.'
        Wait-Guest {@(Isolated-Processes $Identifier|Where-Object {$_.ExecutablePath -ieq $Application -and $_.ProcessId -ne $oldId}).Count -eq 1} 20 'one new process at the same installed path'
        $new=@(Isolated-Processes $Identifier|Where-Object {$_.ExecutablePath -ieq $Application -and $_.ProcessId -ne $oldId})[0]
        $active=Get-Process -Id $new.ProcessId
        Run $Application @('--toolnest-build-info') 'updated-identity-011'
        $identity=Read-State (Join-Path $Evidence 'updated-identity-011.stdout.txt')
        Check ($identity.version -eq '0.1.1' -and $identity.channel -eq 'test') 'Updated installed executable identity mismatch.'
        Wait-Guest {(Get-ItemProperty -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ToolNest').DisplayVersion -eq '0.1.1'} 15 'installed version registration synchronized'
        $stateAfter=Read-State $settingsFile
        foreach($field in @('ScheduleEnabled','ScheduleMode','IntervalHours','DailyHour','DailyMinute','SnapshotDir','ProxyMode','ProxyURL','ProxyUsername','OnboardingDone','AutoCheck','AutoDownload','LaunchAtLogin','StartInTray','CloseBehavior','Theme')){
            Check ($stateAfter.settings.$field -ceq $stateBefore.settings.$field) "Persisted setting changed across update: $field"
        }
        foreach($field in @('enabled','mode','hours','hour','minute','revision','next')){
            Check ($stateAfter.schedule.$field -ceq $stateBefore.schedule.$field) "Persisted schedule changed across update: $field"
        }
        Check ($stateAfter.schedule.enabled -and [DateTimeOffset]::Parse($stateAfter.schedule.next) -gt [DateTimeOffset]::UtcNow) 'Persisted schedule did not resume with its next run.'
        Check ($stateAfter.credential_id -ceq $stateBefore.credential_id) 'Stored proxy credential reference changed across update.'
        foreach($entry in $snapshotBefore.Keys){Check ((Get-FileHash -LiteralPath ($snapshots+$entry)).Hash -ceq $snapshotBefore[$entry]) 'Original snapshot bytes changed across update.'}
        $null=UI $active.Id 'Invoke' '应用设置'
        $null=UI $active.Id 'Invoke' '使用草稿测试连接'
        Wait-UI $active.Id '^更新入口连接正常' 35
        Json 'at18-after.json' @{version=$identity.version;channel=$identity.channel;pid=$active.Id;oldPid=$oldId;installedPath=$Application;switchPhase=$journal.phase;schedule=$stateAfter.schedule;snapshotHashes=(FileTree $snapshots);registrationVersion='0.1.1';proxyProtocol='http';credentialsExported=$false}
        $confirmed=$true
        Record 'AT-18-HTTP-installed-GUI' 'pass' 'Installed 0.1.0: UI schedule/backup/authenticated HTTP proxy/test/check/notes/signed staging/defer/restart; same installed path 0.1.1 first-frame confirmed, registration/snapshot bytes/schedule and stored proxy credentials resumed.'
    }catch{
        $status=if($_.Exception.Message -match '^(UIA_BLOCKED:|BLOCKED:)'){'blocked'}else{'fail'}
        Record 'AT-18-HTTP-installed-GUI' $status $_.Exception.Message
        Json 'at18-manual.json' @{status=$status;step=$_.Exception.Message;dataRoot=$Data;testRoot=$Identifier;restartRequested=$restartRequested;instructions='Only after explicit Sandbox authorization: complete the blocked UI step, preserve original snapshots/settings, verify same installed executable 0.1.1, confirmed switch and version registration; do not edit prepared/settings/switch to manufacture success.'}
    }finally{
        if($null -ne $active){try{Exit-GuestWindow $active}catch{$safe=$false;Record 'AT-18-normal-exit' 'blocked' 'Normal UI exit unconfirmed; finish it manually in Sandbox. No forced kill or installer maintenance.'}}
        if($restartRequested -and !$confirmed){$safe=$false}
        if(@(Isolated-Processes $Identifier).Count){$safe=$false;Record 'AT-17-maintenance' 'blocked' 'Isolated application/helper still running; skip cover-install/uninstall/reinstall, never interrupt a switch.'}
        if($null -ne (Get-Variable fixture -ValueOnly -ErrorAction SilentlyContinue)){Record 'guest-fixture' 'pass' 'Pre-signed archive only; authenticated numeric-loopback fixture expires after five minutes. No private key exported; no force-kill cleanup.'}
    }
    return $safe
}
function Run-WindowEvidence([string]$Application,[string]$Data,[string]$Identifier) {
    $helper=Join-Path $InputRoot 'Window-Automation.ps1'
    $arguments=@('--data-dir',$Data,'--test-root',$Identifier)
    # 仅 Sandbox GUI 可见；端口夹具是本次客体验收的有界子进程，不在宿主挂服务。
    $window=Start-Process -FilePath $Application -ArgumentList $arguments -PassThru
    try{
        $deadline=[DateTime]::UtcNow.AddSeconds(15)
        do{$window.Refresh();if($window.HasExited){throw "ToolNest GUI exited $($window.ExitCode)"};Start-Sleep -Milliseconds 200}while($window.MainWindowHandle -eq 0 -and [DateTime]::UtcNow -lt $deadline)
        & $helper -ProcessId $window.Id -Output (Join-Path $Evidence 'window-initial.png') -Action Inspect
        try{& $helper -ProcessId $window.Id -Output (Join-Path $Evidence 'onboarding-skip.png') -Action Invoke -ControlName '稍后再说'}catch{Record 'AT-10-GUI' 'blocked' $_.Exception.Message}
        foreach($size in @(@(1180,780),@(720,620))){& $helper -ProcessId $window.Id -Output (Join-Path $Evidence ('window-'+$size[0]+'.png')) -Action Resize -Width $size[0] -Height $size[1]}
        try{& $helper -ProcessId $window.Id -Output (Join-Path $Evidence 'keyboard-tabs.png') -Action Keyboard -TabCount 12}catch{Record 'AT-20-keyboard' 'blocked' $_.Exception.Message}
        Record 'AT-20-screenshot' 'pass' 'Captured actual ToolNest-owned window with PrintWindow, actual DPI and UIAutomation tree. Visual review is not implied.'
        Record 'AT-20-matrix' 'blocked' 'Theme, reduced motion and 125/150/200% must be checked at actual session DPI; resizing or bitmap scaling does not count.'
    } catch{Record 'AT-20-window' 'blocked' $_.Exception.Message}
    finally{
        $window.Refresh()
        if(!$window.HasExited){
            [void]$window.CloseMainWindow();Start-Sleep -Milliseconds 300
            try{
                & $helper -ProcessId $window.Id -Output (Join-Path $Evidence 'normal-exit.png') -Action Select -ControlName '退出应用，停止应用内计划'
                & $helper -ProcessId $window.Id -Output (Join-Path $Evidence 'confirm-exit.png') -Action Invoke -ControlName '确认关闭'
                if(!$window.WaitForExit(5000)){throw 'Normal application exit did not complete.'}
                Record 'guest-ui-cleanup' 'pass' 'UI-selected normal exit completed; no force kill.'
            }catch{Record 'guest-ui-cleanup' 'blocked' 'Normal exit could not be confirmed. Finish explicit exit in Sandbox; no forced termination.'}
        }
    }
}
try {
    Json 'guest-environment.json' @{os=[Environment]::OSVersion.VersionString; account=$env:USERNAME; date=(Get-Date).ToString('o'); networking='disabled; loopback fixtures only'}
    if ($Probe) { Record 'sandbox-probe' 'pass' 'Guest logon command executed under WDAGUtilityAccount; writable evidence mapping verified.' }
    else {
        $signedInputs=@('update-0.1.1.tar.gz','update-0.1.1.sig','update.pub')
        $hasSignedInputs=@($signedInputs|Where-Object {!(Test-Path -LiteralPath (Join-Path $InputRoot $_))}).Count -eq 0
        if($hasSignedInputs){$env:TOOLNEST_ACCEPTANCE_SIGNED_UPDATE=$InputRoot}
        Run (Join-Path $InputRoot 'acceptance.test.exe') @('-test.v','-test.timeout=120s') 'guest-integration'
        Record 'isolated-integration' 'pass' 'Precompiled tests execute without a Go installation in Sandbox.'
        $coreLog=Get-Content -LiteralPath (Join-Path $Evidence 'guest-integration.stdout.txt') -Raw
        if($hasSignedInputs -and $coreLog -match '--- PASS: TestOptionalActualSignedApplicationStaging/socks5'){
            Record 'AT-18-SOCKS5-core-staging' 'pass' 'Actual signed PE downloaded and verified through authenticated SOCKS5 by core test; not an installed GUI lifecycle.'
        }else{Record 'AT-18-SOCKS5-core-staging' 'blocked' 'No actual signed SOCKS5 staging PASS evidence in guest integration output; never infer it from HTTP GUI.'}
        $installer010=Join-Path $InputRoot 'ToolNest-Setup-0.1.0.exe';$installer011=Join-Path $InputRoot 'ToolNest-Setup-0.1.1.exe'
        if (!(Test-Path -LiteralPath $installer010) -or !(Test-Path -LiteralPath $installer011)) {
            Record 'AT-17/18' 'blocked' 'Both actual signed test installers 0.1.0 and 0.1.1 were not supplied; synthetic payload tests cannot substitute.'
        } else {
            $beforeUser=[Environment]::GetEnvironmentVariable('Path','User');$beforeSystem=[Environment]::GetEnvironmentVariable('Path','Machine')
            $install=Join-Path $env:LOCALAPPDATA 'Programs\ToolNest';$data=Join-Path $env:LOCALAPPDATA 'ToolNest';$legacy=Join-Path $env:LOCALAPPDATA 'PathGuard'
            [void][IO.Directory]::CreateDirectory($data);[void][IO.Directory]::CreateDirectory($legacy)
            [IO.File]::WriteAllText((Join-Path $data 'acceptance.keep'),'synthetic user data; must survive')
            [IO.File]::WriteAllText((Join-Path $legacy 'acceptance.keep'),'synthetic legacy data; must survive')
            $dataBefore=FileTree $data;$legacyBefore=FileTree $legacy
            foreach($installer in @($installer010,$installer011)) {
                $signature=Get-AuthenticodeSignature -LiteralPath $installer
                Record ([IO.Path]::GetFileName($installer)+'-Authenticode') $(if($signature.Status -eq 'Valid'){'pass'}else{'blocked'}) $signature.Status.ToString()
            }
            Run $installer010 @('/S') 'install-010'
            $application=Join-Path $install 'ToolNest.exe'
            Run $application @('--toolnest-build-info') 'identity-010'
            $identity=Get-Content -LiteralPath (Join-Path $Evidence 'identity-010.stdout.txt') -Raw | ConvertFrom-Json
            Check ($identity.version -eq '0.1.0' -and $identity.channel -eq 'test' -and $identity.publicKey) 'Expected installed test 0.1.0 identity and pinned public key.'
            $isolationId=[guid]::NewGuid().ToString('N')
            try{
                Run $application @('--data-dir',(Join-Path $Work 'installed-data'),'--test-root',$isolationId,'--diagnostics-json') 'installed-test-mode'
                Record 'installed-test-interface' 'pass' 'Actual installed binary accepted paired isolation parameters.'
            }catch{Record 'installed-test-interface' 'blocked' 'Installed binary rejected the paired test interface. See installed-test-mode.stderr.txt; do not bypass production path guard.'}
            $registration=Get-ItemProperty -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ToolNest'
            Check ($registration.DisplayVersion -eq '0.1.0') 'Uninstall version 0.1.0 mismatch.'
            $maintenanceSafe=Run-InstalledUpdate $application (Join-Path $Work 'installed-data') $isolationId
            if(!$maintenanceSafe){throw 'BLOCKED: installed application/update helper exit unconfirmed; installer maintenance skipped.'}
            $isolatedBefore=FileTree (Join-Path $Work 'installed-data')
            [IO.File]::WriteAllText((Join-Path $install 'unknown.keep'),'must not remove unknown file')
            Run $installer011 @('/S') 'cover-install-011'
            Run $application @('--toolnest-build-info') 'identity-011'
            $identity=Get-Content -LiteralPath (Join-Path $Evidence 'identity-011.stdout.txt') -Raw | ConvertFrom-Json
            Check ($identity.version -eq '0.1.1' -and $identity.channel -eq 'test') 'Installed version did not change to 0.1.1.'
            $run='HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
            [void](New-Item -Path $run -Force)
            New-ItemProperty -LiteralPath $run -Name 'ToolNest' -Value ('"'+$application+'" --autostart') -PropertyType String -Force | Out-Null
            New-ItemProperty -LiteralPath $run -Name 'ToolNestAcceptanceUnrelated' -Value 'acceptance-only-unrelated-entry' -PropertyType String -Force | Out-Null
            Run (Join-Path $install 'Uninstall.exe') @('/S',('_?='+$install)) 'uninstall-011'
            Check (!(Test-Path -LiteralPath $application)) 'Uninstall retained owned executable.'
            Check (Test-Path -LiteralPath (Join-Path $install 'unknown.keep')) 'Uninstall removed unknown file.'
            Check (!(Test-Path -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ToolNest')) 'Uninstall registration remains.'
            $runValues=Get-ItemProperty -LiteralPath $run
            Check (!($runValues.PSObject.Properties.Name -contains 'ToolNest')) 'Owned Run entry was not removed.'
            Check ($runValues.ToolNestAcceptanceUnrelated -eq 'acceptance-only-unrelated-entry') 'Uninstall removed unrelated Run entry.'
            foreach($entry in $dataBefore.Keys) { Check ((Get-FileHash -LiteralPath ($data+$entry)).Hash -eq $dataBefore[$entry]) 'User data changed during install/uninstall.' }
            foreach($entry in $legacyBefore.Keys) { Check ((Get-FileHash -LiteralPath ($legacy+$entry)).Hash -eq $legacyBefore[$entry]) 'Legacy data changed during uninstall.' }
            Run $installer011 @('/S') 'reinstall-011'
            foreach($entry in $isolatedBefore.Keys){Check ((Get-FileHash -LiteralPath ((Join-Path $Work 'installed-data')+$entry)).Hash -ceq $isolatedBefore[$entry]) 'Installed acceptance settings/snapshots changed during cover-install/uninstall/reinstall.'}
            $runValues=Get-ItemProperty -LiteralPath $run
            Check (!($runValues.PSObject.Properties.Name -contains 'ToolNest')) 'Reinstall silently re-enabled autostart.'
            Check (([Environment]::GetEnvironmentVariable('Path','User') -ceq $beforeUser) -and ([Environment]::GetEnvironmentVariable('Path','Machine') -ceq $beforeSystem)) 'Installer changed PATH.'
            Record 'AT-17' 'pass' 'Actual per-user install/cover-upgrade/uninstall/reinstall completed; version/unknown file/data/PATH verified.'
        }
        $applicationInput=Join-Path $InputRoot 'ToolNest.exe'
        if (Test-Path -LiteralPath $applicationInput) {
            $localProgram=Join-Path $Work 'program';[void][IO.Directory]::CreateDirectory($localProgram);Copy-Item -LiteralPath $applicationInput -Destination (Join-Path $localProgram 'ToolNest.exe')
            $isolatedData=Join-Path $Work 'ui-data';$identifier=[guid]::NewGuid().ToString('N')
            Run (Join-Path $localProgram 'ToolNest.exe') @('--data-dir',$isolatedData,'--test-root',$identifier,'--diagnostics-json') 'isolated-diagnostics'
            Record 'entry-diagnostics' 'pass' 'Actual paired --data-dir/--test-root diagnostics executed.'
            Run-WindowEvidence (Join-Path $localProgram 'ToolNest.exe') $isolatedData $identifier
        } else { Record 'AT-20' 'blocked' 'Application executable missing; no real ToolNest window screenshot exists.' }
        Record 'AT-05-UAC' 'blocked' 'Secure desktop cancellation and another administrator account require safe interactive verification; never auto-approve UAC.'
    }
} catch { Record 'execution' $(if($_.Exception.Message -match '^(BLOCKED:|UIA_BLOCKED:)'){'blocked'}else{'fail'}) $_.Exception.Message; $_ | Out-String | Set-Content -LiteralPath (Join-Path $Evidence 'guest-exception.txt') -Encoding UTF8 }
finally {
    $status=if(@($Results|Where-Object status -eq 'fail').Count){'fail'}elseif(@($Results|Where-Object status -eq 'blocked').Count){'blocked'}else{'pass'}
    Json 'guest-result.json' @{status=$status;complete=$false;results=@($Results)}
}
