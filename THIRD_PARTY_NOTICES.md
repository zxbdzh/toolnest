# 第三方软件

ToolNest 使用以下开源组件。各组件的代码和许可归原作者所有，具体版本以 `go.mod`、`go.sum` 为准。

| 组件 | 用途 | 许可与来源 |
| --- | --- | --- |
| MyGo | 原生桌面界面与系统集成 | [MIT](https://github.com/egoist/mygo/blob/main/LICENSE) |
| purego | Go 原生系统调用支持 | [Apache-2.0](https://github.com/ebitengine/purego/blob/main/LICENSE) |
| go-text/typesetting | 文字排版 | [Unlicense 或 BSD-3-Clause](packaging/licenses/github.com_go-text_typesetting.txt) |
| golang.org/x/image、x/net、x/sys、x/text | 图形、网络、系统与文本能力 | [Go BSD-style](https://go.dev/LICENSE) |
| NSIS | Windows 安装器 | [NSIS 许可证](https://nsis.sourceforge.io/License) |
| akavel/rsrc | 构建时生成 Windows 图标资源 | [MIT](https://github.com/akavel/rsrc/blob/master/LICENSE.txt) |

产品图标由 ToolNest 自行绘制。BewlyCat、PearCleaner 与 Wintoys 仅作界面与交互调研参考，未将其品牌标识或媒体资源作为产品资产分发。

运行依赖的完整许可原文保存在 [packaging/licenses](packaging/licenses)，并随安装器一起安装。构建工具自身按原项目许可使用。
