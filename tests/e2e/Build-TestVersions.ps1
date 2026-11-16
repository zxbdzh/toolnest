#requires -Version 7.2
[CmdletBinding()]
param(
    [string]$Go='go.exe',
    [Parameter(Mandatory)][string]$MakeNSIS,
    [ValidatePattern('^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$')]
    [string]$UpdateRepository='zxbdzh/toolnest-updates-test',
    [string]$SigningKeyDirectory,
    [switch]$PublishBundles
)
Set-StrictMode -Version Latest
$ErrorActionPreference='Stop'
$Repository=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
$sourcePolicy=Get-Content -LiteralPath (Join-Path $PSScriptRoot 'TestUpdateSource.json') -Raw|ConvertFrom-Json
if($UpdateRepository-ceq $sourcePolicy.repository-and !$SigningKeyDirectory){throw 'Published test source requires -SigningKeyDirectory pointing to its existing external TEST-ONLY key; do not silently rotate its pinned public key.'}
if($SigningKeyDirectory){
    $SigningKeyDirectory=[IO.Path]::GetFullPath($SigningKeyDirectory)
    if($SigningKeyDirectory-eq $Repository-or $SigningKeyDirectory.StartsWith($Repository+'\',[StringComparison]::OrdinalIgnoreCase)){throw 'Signing key directory must stay outside the repository.'}
    foreach($name in @('TEST-ONLY','update.key','update.pub')){if(!(Test-Path -LiteralPath (Join-Path $SigningKeyDirectory $name) -PathType Leaf)){throw 'Existing external TEST-ONLY signing key files are required.'}}
    if($UpdateRepository-ceq $sourcePolicy.repository-and (Get-Content -LiteralPath (Join-Path $SigningKeyDirectory 'update.pub') -Raw).Trim()-cne $sourcePolicy.publicKey){throw 'Existing signing public key differs from the pinned published test source; key rotation requires a new baseline.'}
}
 # 签名工具只接受系统临时验收根内的输出；沿用 runner 的 reserved 约定，避免夹具构建中途被拒绝。
 $root=Join-Path (Join-Path ([IO.Path]::GetTempPath()) 'toolnest-acceptance') ('versions-'+(Get-Date -Format yyyyMMdd-HHmmss)+'-'+[guid]::NewGuid().ToString('N').Substring(0,8))
[void][IO.Directory]::CreateDirectory($root)
$Utf8=[Text.UTF8Encoding]::new($false)
function Invoke-Checked([string]$File,[string[]]$Arguments){& $File @Arguments;if($LASTEXITCODE){throw "Command failed ($LASTEXITCODE): $File"}}
$saved=@{};foreach($name in @('GOTOOLCHAIN','GOWORK','GOFLAGS')){$saved[$name]=[Environment]::GetEnvironmentVariable($name)}
try {
    $env:GOTOOLCHAIN='local';$env:GOWORK='off';$env:GOFLAGS='-mod=readonly'
    if (!(Test-Path -LiteralPath $MakeNSIS -PathType Leaf)) { throw 'Explicit existing makensis.exe required.' }
    $goIdentity=& $Go version
    if ($goIdentity -cne 'go version go1.27.4 windows/amd64') { throw 'Exact Go 1.27.4 windows/amd64 is required.' }
    Push-Location $Repository
    try {
        $runner=Join-Path $root 'acceptance-runner.exe'
        Invoke-Checked $Go @('build','-trimpath','-buildvcs=false','-o',$runner,'./tests/e2e/runner')
        # 已公开源必须复用原签名身份；临时夹具可生成新密钥，但不能悄悄改变已安装基线的信任链。
        $keys=$SigningKeyDirectory
        if(!$keys){$keys=Join-Path $root 'private-test-keys';Invoke-Checked $runner @('--operation','keygen','--out',$keys)}
        $public=(Get-Content -LiteralPath (Join-Path $keys 'update.pub') -Raw).Trim()
        $brandAssets=Join-Path $root 'brand-assets'
        & (Join-Path $Repository 'scripts\Build-BrandAssets.ps1') -Output $brandAssets -Go $Go
        $versionSource=Join-Path $Repository 'packaging\version.go'
        $original=Get-Content -LiteralPath $versionSource -Raw
        $anchor='return strings.TrimSpace(version)'
        if (($original.Split($anchor)).Count -ne 2) { throw 'Version API changed; update test overlay deliberately instead of patching shared sources.' }
        foreach($version in @('0.1.0','0.1.1')) {
            $stage=Join-Path $root $version;[void][IO.Directory]::CreateDirectory($stage)
            Copy-Item -LiteralPath (Join-Path $brandAssets 'ToolNest.ico') -Destination $stage
            # Go overlay 只改编译器视图，主仓 VERSION、入口和他人工作树保持原样。
            $replacement=Join-Path $stage 'version-overlay.go'
            [IO.File]::WriteAllText($replacement,$original.Replace($anchor,('return strings.TrimSpace("'+$version+'")')),$Utf8)
            $overlay=Join-Path $stage 'overlay.json';$mapping=@{};$mapping[$versionSource]=$replacement
            [IO.File]::WriteAllText($overlay,(@{Replace=$mapping}|ConvertTo-Json -Depth 4),$Utf8)
            $flags="-s -w -H=windowsgui -X toolnest/packaging.Repository=$UpdateRepository -X toolnest/packaging.UpdatePublicKey=$public -X toolnest/packaging.BuildChannel=test"
            $application=Join-Path $stage 'ToolNest.exe'
            Invoke-Checked $Go @('build','-trimpath','-buildvcs=false',('-overlay='+$overlay),'-ldflags',$flags,'-o',$application,'.')
            & (Join-Path $Repository 'scripts\Assert-WindowsGui.ps1') -Path $application
            Invoke-Checked $Go @('build','-trimpath','-buildvcs=false','-o',(Join-Path $stage 'ToolNest-maintenance.exe'),'./packaging/maintenance')
            # 只读身份先于 UI 与注册表写入；绝不在宿主启动正常应用或安装器。
            $identityText=& (Join-Path $Repository 'scripts\Read-BuildInfo.ps1') -Path $application
            $identity=$identityText|ConvertFrom-Json
            if($identity.version -cne $version -or $identity.repository -cne $UpdateRepository -or $identity.channel -cne 'test' -or $identity.publicKey -cne $public -or $identity.runtimeProtocol -ne 1){throw 'Real application build identity mismatch.'}
            [IO.File]::WriteAllText((Join-Path $stage 'release-identity.json'),($identityText -join "`n"),$Utf8)
            $installer=Join-Path $stage ('ToolNest-Setup-'+$version+'.exe')
            Invoke-Checked $MakeNSIS @('/V3','/INPUTCHARSET','UTF8',('/DVERSION='+$version),('/DPAYLOAD='+$stage),('/DOUTPUT='+$installer),(Join-Path $Repository 'packaging\toolnest.nsi'))
            if($PublishBundles){
                # 发布目录只允许经过独立公钥校验的八个白名单资产，不含构建缓存、源码、截图或私钥。
                $publish=Join-Path $stage 'publish';[void][IO.Directory]::CreateDirectory($publish)
                Copy-Item -LiteralPath $installer -Destination (Join-Path $publish ('ToolNest-Setup-'+$version+'-windows-amd64.exe'))
                Copy-Item -LiteralPath (Join-Path $stage 'release-identity.json') -Destination $publish
                $releaseTool=Join-Path $stage 'release-tool.exe'
                Invoke-Checked $Go @('build','-trimpath','-buildvcs=false',('-overlay='+$overlay),'-o',$releaseTool,'./scripts/release')
                Invoke-Checked $releaseTool @('bundle','--test','--root',$Repository,'--dir',$publish,'--exe',$application,'--repository',$UpdateRepository,'--public-key',$public,'--key-file',(Join-Path $keys 'update.key'))
                Invoke-Checked $releaseTool @('verify','--test','--dir',$publish,'--repository',$UpdateRepository,'--public-key',$public)
            }
        }
        $signed=Join-Path $root 'signed-update'
        Invoke-Checked $runner @('--operation','sign','--out',$signed,'--version','0.1.1','--exe',(Join-Path $root '0.1.1\ToolNest.exe'),'--key-file',(Join-Path $keys 'update.key'))
        Invoke-Checked $runner @('--operation','sign','--out',(Join-Path $root 'signed-0.1.0'),'--version','0.1.0','--exe',(Join-Path $root '0.1.0\ToolNest.exe'),'--key-file',(Join-Path $keys 'update.key'))
        $result=@{status='pass';scope='test update signatures only; NSIS Authenticode not configured';repository=$UpdateRepository;publishBundles=[bool]$PublishBundles;version010=Join-Path $root '0.1.0\ToolNest-Setup-0.1.0.exe';version011=Join-Path $root '0.1.1\ToolNest-Setup-0.1.1.exe';application010=Join-Path $root '0.1.0\ToolNest.exe';signedUpdate=$signed;privateKeysMappedToGuest=$false;productionRelease='blocked'}
        [IO.File]::WriteAllText((Join-Path $root 'fixtures.json'),($result|ConvertTo-Json -Depth 4),$Utf8)
        $result|ConvertTo-Json -Depth 4
    } finally {Pop-Location}
} finally {foreach($name in $saved.Keys){[Environment]::SetEnvironmentVariable($name,$saved[$name])}}
