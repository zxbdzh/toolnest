$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = [Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
try {
    # 所有可变输入只经 JSON 读取，绝不拼接命令或执行输入的 XML。
    $request = [Console]::In.ReadToEnd() | ConvertFrom-Json
    if ($request.operation -cne 'read' -and $request.operation -cne 'toggle') { throw 'Unsupported operation' }
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $sid = $identity.User.Value
    $name = 'PathGuard-PATH-Backup-' + $sid
    if ($request.sid -cne $sid -or $request.name -cne $name) { throw 'Current-user task mismatch' }
    $service = New-Object -ComObject 'Schedule.Service'
    $service.Connect()
    $folder = $service.GetFolder('\')
    function Read-KnownTask {
        try { return $folder.GetTask($name) } catch {
            $root = $_.Exception
            while ($null -ne $root.InnerException) { $root = $root.InnerException }
            if ($root.HResult -eq -2147024894) { return $null }
            throw
        }
    }
    function Describe-Task($task) {
        if ($null -eq $task) { return @{ exists = $false; name = $name } }
        $definition = $task.Definition
        $action = $null
        if ($definition.Actions.Count -eq 1) { $action = $definition.Actions.Item(1) }
        return @{
            exists = $true; name = $name; xml = $task.Xml; enabled = [bool]$task.Enabled
            userID = $definition.Principal.UserId; author = $definition.RegistrationInfo.Author
            source = $definition.RegistrationInfo.Source; description = $definition.RegistrationInfo.Description
            executable = $(if ($null -ne $action -and $action.Type -eq 0) { $action.Path } else { '' })
            arguments = $(if ($null -ne $action -and $action.Type -eq 0) { $action.Arguments } else { '' })
            actionCount = [int]$definition.Actions.Count; actionType = $(if ($null -ne $action) { [int]$action.Type } else { -1 })
            logonType = [int]$definition.Principal.LogonType; runLevel = [int]$definition.Principal.RunLevel
        }
    }
    function Assert-Owned($state) {
        $marker = 'PathGuard/RegistryPathBackup/v1/49b84af9-b152-49f9-9de1-6ee0d93adfd6'
        $pathOK = $false
        foreach ($exe in $request.executables) { if ([string]::Equals($state.executable, $exe, [StringComparison]::OrdinalIgnoreCase)) { $pathOK = $true } }
        if (-not $state.exists -or -not $pathOK -or $state.source -cne $marker -or $state.description -cne $marker -or
            $state.author -cne ('PathGuard:' + $sid) -or ($state.userID -ine $sid -and $state.userID -ine $identity.Name) -or
            $state.actionCount -ne 1 -or $state.actionType -ne 0 -or $state.arguments -cne '--backup' -or
            $state.logonType -ne 3 -or $state.runLevel -ne 0) { throw 'Ownership not confirmed; task unchanged' }
    }
    $task = Read-KnownTask
    $state = Describe-Task $task
    if ($request.operation -ceq 'toggle') {
        Assert-Owned $state
        if ($state.xml -cne $request.expectedXML -or $state.enabled -ne [bool]$request.expectedEnabled) { throw 'Task changed; refresh confirmation' }
        # 最后再取一遍定义；只改 Enabled，不删除/重建任务，也不停止已运行进程。
        $task = Read-KnownTask
        $state = Describe-Task $task
        Assert-Owned $state
        if ($state.xml -cne $request.expectedXML -or $state.enabled -ne [bool]$request.expectedEnabled) { throw 'Task changed before toggle' }
        $task.Enabled = [bool]$request.enabled
        $after = Describe-Task (Read-KnownTask)
        Assert-Owned $after
        if ($after.enabled -ne [bool]$request.enabled) { throw 'Task readback mismatch' }
        # XML 的 Enabled 可能由 COM 同步；除此之外的定义变化必须报冲突，不能盲目回滚。
        function Stable-Xml([string]$text) {
            $doc = New-Object System.Xml.XmlDocument
            $doc.XmlResolver = $null
            $doc.LoadXml($text)
            $ns = New-Object System.Xml.XmlNamespaceManager($doc.NameTable)
            $ns.AddNamespace('t', 'http://schemas.microsoft.com/windows/2004/02/mit/task')
            $node = $doc.SelectSingleNode('/t:Task/t:Settings/t:Enabled', $ns)
            if ($null -ne $node) { $node.InnerText = 'false' }
            return $doc.OuterXml
        }
        if ((Stable-Xml $state.xml) -cne (Stable-Xml $after.xml)) { throw 'Task definition changed; backup retained' }
        $state = $after
    }
    $state | ConvertTo-Json -Compress -Depth 8
    exit 0
} catch {
    [Console]::Error.WriteLine($_.Exception.Message)
    exit 1
}
