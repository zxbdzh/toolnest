#requires -Version 5.1
[CmdletBinding()]
param(
    [Parameter(Mandatory)][int]$ProcessId,
    [string]$Output=(Join-Path ([IO.Path]::GetTempPath()) 'toolnest-window.png'),
    [ValidateSet('Capture','Inspect','Keyboard','Invoke','SetValue','Select','Toggle','Scroll','Resize','Close')][string]$Action='Capture',
    [string]$ControlName,[string]$Value,[int]$Width=1180,[int]$Height=780,[int]$TabCount=12,
    [switch]$NoCapture
)
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
$guest=$env:USERNAME -eq 'WDAGUtilityAccount' -and $PSScriptRoot -eq 'C:\ToolNestAcceptance\input'
if (!$guest) { throw 'All window capture, UIAutomation, focus and keyboard operations are restricted to Windows Sandbox.' }
$account=[Security.Principal.WindowsIdentity]::GetCurrent().Name.Split('\')[-1]
if ($account -ine 'WDAGUtilityAccount') { throw 'All window operations are restricted to Windows Sandbox; actual token account mismatch.' }
$process=Get-Process -Id $ProcessId -ErrorAction Stop
if ($process.ProcessName -ne 'ToolNest') { throw 'Screenshot helper only accepts a ToolNest PID.' }
$Output=[IO.Path]::GetFullPath($Output)
$allowed='C:\ToolNestAcceptance\evidence\'
if (!$Output.StartsWith($allowed,[StringComparison]::OrdinalIgnoreCase)) { throw 'Evidence must stay in the reserved acceptance directory.' }
Add-Type -AssemblyName System.Drawing
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class ToolNestWindow {
  public delegate bool EnumWindowsProc(IntPtr handle, IntPtr state);
  [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindowsProc callback, IntPtr state);
  [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr window, out uint processId);
  [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr window);
  [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr window, out RECT rect);
  [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr window, IntPtr dc, uint flags);
  [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr window);
  [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
  [DllImport("user32.dll")] public static extern bool SetWindowPos(IntPtr window, IntPtr after, int x, int y, int width, int height, uint flags);
  [DllImport("user32.dll")] public static extern uint GetDpiForWindow(IntPtr window);
  [DllImport("dwmapi.dll")] public static extern int DwmGetWindowAttribute(IntPtr window, int attribute, out RECT rect, int size);
  [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left,Top,Right,Bottom; }
}
'@
$candidates=[Collections.Generic.List[object]]::new()
$callback=[ToolNestWindow+EnumWindowsProc]{
    param([IntPtr]$window,[IntPtr]$state)
    [uint32]$owner=0
    [void][ToolNestWindow]::GetWindowThreadProcessId($window,[ref]$owner)
    if ($owner -eq $ProcessId -and [ToolNestWindow]::IsWindowVisible($window)) {
        $rect=[ToolNestWindow+RECT]::new()
        if ([ToolNestWindow]::DwmGetWindowAttribute($window,9,[ref]$rect,[Runtime.InteropServices.Marshal]::SizeOf([type][ToolNestWindow+RECT])) -ne 0) { [void][ToolNestWindow]::GetWindowRect($window,[ref]$rect) }
        if ($rect.Right -gt $rect.Left -and $rect.Bottom -gt $rect.Top) { $candidates.Add(@{handle=$window;rect=$rect}) }
    }
    return $true
}
[void][ToolNestWindow]::EnumWindows($callback,[IntPtr]::Zero)
if ($candidates.Count -eq 0) { throw "No visible ToolNest-owned top-level window for PID $ProcessId." }
$window=$candidates | Sort-Object {($_.rect.Right-$_.rect.Left)*($_.rect.Bottom-$_.rect.Top)} -Descending | Select-Object -First 1
if($Action -eq 'Close'){[void]$process.CloseMainWindow();Write-Output 'Requested normal application close; no termination or tray assumption.';return}
if ($Action -eq 'Resize') {
    if ($Width -lt 620 -or $Width -gt 2200 -or $Height -lt 520 -or $Height -gt 1600) { throw 'Unsafe window size.' }
    if (![ToolNestWindow]::SetWindowPos($window.handle,[IntPtr]::Zero,0,0,$Width,$Height,0x16)) { throw 'Resize rejected.' }
    Start-Sleep -Milliseconds 250
    $rect=[ToolNestWindow+RECT]::new();[void][ToolNestWindow]::GetWindowRect($window.handle,[ref]$rect);$window.rect=$rect
}
$uiaResult=@{status='blocked';reason='UIAutomation not inspected'}
try {
    Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes
    $root=[Windows.Automation.AutomationElement]::FromHandle($window.handle)
    if ($root.Current.ProcessId -ne $ProcessId) { throw 'UIAutomation ownership mismatch.' }
    $condition=[Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::ProcessIdProperty,$ProcessId)
    $elements=$root.FindAll([Windows.Automation.TreeScope]::Descendants,$condition)
    $controls=@()
    $matches=[Collections.Generic.List[object]]::new()
    foreach($element in $elements) {
        $current=$element.Current
        # 不导出 Value/Text 内容和代理密码，控件名中的个人路径也脱敏。
        $safeName=$current.Name -replace '(?i)[A-Z]:\\[^\r\n]*','[path redacted]'
        $controls+=@{name=$safeName;id=$current.AutomationId;type=$current.ControlType.ProgrammaticName;focusable=$current.IsKeyboardFocusable;offscreen=$current.IsOffscreen;enabled=$current.IsEnabled}
        if ($ControlName -and $current.Name -ceq $ControlName -and $current.ControlType -ne [Windows.Automation.ControlType]::Text) {
            $patterns=switch($Action){
                'Invoke' {@([Windows.Automation.InvokePattern]::Pattern)}
                'SetValue' {@([Windows.Automation.ValuePattern]::Pattern)}
                'Select' {@([Windows.Automation.SelectionItemPattern]::Pattern,[Windows.Automation.InvokePattern]::Pattern)}
                'Toggle' {@([Windows.Automation.TogglePattern]::Pattern)}
                'Scroll' {@([Windows.Automation.ScrollItemPattern]::Pattern,[Windows.Automation.ScrollPattern]::Pattern,[Windows.Automation.InvokePattern]::Pattern,[Windows.Automation.ValuePattern]::Pattern,[Windows.Automation.SelectionItemPattern]::Pattern,[Windows.Automation.TogglePattern]::Pattern)}
                default {@()}
            }
            foreach($supported in $patterns){
                $candidatePattern=$null
                if($element.TryGetCurrentPattern($supported,[ref]$candidatePattern)){
                    if($Action -ne 'SetValue' -or !$candidatePattern.Current.IsReadOnly){$matches.Add($element)}
                    break
                }
            }
        }
    }
    if ($Action -in @('Invoke','SetValue','Select','Toggle','Scroll')) {
        # 先筛掉同名静态标签，再确认唯一交互元素；绝不边遍历边重复点击。
        if (!$ControlName -or $matches.Count -ne 1) { throw "Requested unique interactive control unavailable ($($matches.Count) matches): $ControlName; manual Sandbox operation required, no coordinate fallback." }
        $target=$matches[0]
        if(!$target.Current.IsEnabled){throw "Requested control disabled: $ControlName"}
        if($target.Current.IsOffscreen -or $Action -eq 'Scroll'){
            $scrollItem=$null
            if($target.TryGetCurrentPattern([Windows.Automation.ScrollItemPattern]::Pattern,[ref]$scrollItem)){$scrollItem.ScrollIntoView()}
            else{
                # 只遍历该窗口内的祖先滚动容器，不使用桌面坐标、全局滚轮或键鼠。
                $ancestor=$target;$scrolled=$false
                for($depth=0;$depth -lt 16 -and $null -ne $ancestor;$depth++){
                    if($ancestor.Current.ProcessId -ne $ProcessId){break}
                    $scroll=$null
                    if($ancestor.TryGetCurrentPattern([Windows.Automation.ScrollPattern]::Pattern,[ref]$scroll) -and $scroll.Current.VerticallyScrollable){
                        for($attempt=0;$attempt -lt 8;$attempt++){
                            $amount=if($Value -eq 'Up'){[Windows.Automation.ScrollAmount]::SmallDecrement}else{[Windows.Automation.ScrollAmount]::SmallIncrement}
                            $scroll.Scroll([Windows.Automation.ScrollAmount]::NoAmount,$amount);Start-Sleep -Milliseconds 80
                            if(!$target.Current.IsOffscreen){break}
                        }
                        $scrolled=$true;break
                    }
                    if([Windows.Automation.Automation]::Compare($ancestor,$root)){break}
                    $ancestor=[Windows.Automation.TreeWalker]::ControlViewWalker.GetParent($ancestor)
                }
                if(!$scrolled){throw 'Window-local scroll pattern unavailable; manual Sandbox scroll required.'}
            }
            Start-Sleep -Milliseconds 100
            if($target.Current.IsOffscreen){throw "Requested control remains offscreen: $ControlName; manual Sandbox scroll required."}
        }
        $pattern=$null
        switch($Action){
            'Invoke' {$pattern=$target.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern);$pattern.Invoke()}
            'SetValue' {$pattern=$target.GetCurrentPattern([Windows.Automation.ValuePattern]::Pattern);$pattern.SetValue($Value)}
            'Select' {
                # MyGo 的 Segmented/按钮可能仅暴露 Invoke；仅在唯一目标上兼容。
                if($target.TryGetCurrentPattern([Windows.Automation.SelectionItemPattern]::Pattern,[ref]$pattern)){$pattern.Select()}
                elseif($target.TryGetCurrentPattern([Windows.Automation.InvokePattern]::Pattern,[ref]$pattern)){$pattern.Invoke()}
                else{throw 'SelectionItem/Invoke pattern unavailable.'}
            }
            'Toggle' {
                if($Value -notin @('On','Off')){throw 'Toggle requires an explicit On or Off state.'}
                $pattern=$target.GetCurrentPattern([Windows.Automation.TogglePattern]::Pattern)
                if($pattern.Current.ToggleState.ToString() -ne $Value){$pattern.Toggle()}
                if($pattern.Current.ToggleState.ToString() -ne $Value){throw 'Requested toggle state was not reached.'}
            }
        }
    }
    $focusTrace=@()
    if ($Action -eq 'Keyboard') {
        if ($TabCount -lt 1 -or $TabCount -gt 40) { throw 'TabCount must be 1..40.' }
        Add-Type -AssemblyName System.Windows.Forms
        [void][ToolNestWindow]::SetForegroundWindow($window.handle)
        for($index=0;$index -lt $TabCount;$index++) {
            if ([ToolNestWindow]::GetForegroundWindow() -ne $window.handle) { throw 'Foreground changed; keyboard injection stopped.' }
            [Windows.Forms.SendKeys]::SendWait('{TAB}');Start-Sleep -Milliseconds 80
            $focused=[Windows.Automation.AutomationElement]::FocusedElement
            if ($null -eq $focused -or $focused.Current.ProcessId -ne $ProcessId) { throw 'Keyboard focus escaped ToolNest.' }
            $focusTrace+=@{id=$focused.Current.AutomationId;type=$focused.Current.ControlType.ProgrammaticName}
        }
    }
    $uiaResult=@{status='pass';controls=$controls;focusTrace=$focusTrace;limits='Tree and focus evidence do not prove visual usability; manual review is still required.'}
} catch {
    $uiaResult=@{status='blocked';reason=$_.Exception.Message}
    if ($Action -in @('Inspect','Invoke','SetValue','Select','Toggle','Scroll','Keyboard')) { throw }
}
if($NoCapture){
    # 输入临时代理凭据时只保存控件元数据，避免截图或导出 Value/Text。
    $result=@{pid=$ProcessId;action=$Action;automation=$uiaResult;capture='disabled'}
    $result|ConvertTo-Json -Depth 12|Set-Content -LiteralPath ($Output+'.json') -Encoding UTF8
    Write-Output ([pscustomobject]$result);return
}
$bounds=[Drawing.Rectangle]::FromLTRB($window.rect.Left,$window.rect.Top,$window.rect.Right,$window.rect.Bottom)
$bitmap=[Drawing.Bitmap]::new($bounds.Width,$bounds.Height)
$graphics=[Drawing.Graphics]::FromImage($bitmap)
try {
    # 禁止桌面截屏兜底，避免其他窗口覆盖时把个人内容写进验收证据。
    $dc=$graphics.GetHdc()
    try { if (![ToolNestWindow]::PrintWindow($window.handle,$dc,2)) { throw 'PrintWindow unavailable; no desktop screenshot fallback.' } }
    finally { $graphics.ReleaseHdc($dc) }
    $parent=[IO.Path]::GetDirectoryName([IO.Path]::GetFullPath($Output))
    [void][IO.Directory]::CreateDirectory($parent)
    $bitmap.Save([IO.Path]::GetFullPath($Output),[Drawing.Imaging.ImageFormat]::Png)
} finally { $graphics.Dispose();$bitmap.Dispose() }
$dpi=[ToolNestWindow]::GetDpiForWindow($window.handle)
@{pid=$ProcessId;action=$Action;dpi=$dpi;actualScalePercent=($dpi/96*100);width=$bounds.Width;height=$bounds.Height;automation=$uiaResult;capture='PrintWindow; target window only; blank/occluded rendering still needs review'} | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath ($Output+'.json') -Encoding UTF8
Write-Output ([pscustomobject]@{pid=$ProcessId;action=$Action;automation=$uiaResult;capture=$Output})
