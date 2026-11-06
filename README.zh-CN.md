# zotty

[English](README.md) | 简体中文

在终端里使用 Zotero。不用打开 Zotero 就能浏览、搜索文献、管理阅读队列；Zotero 关着的时候，Chrome 里照样能保存论文。

![搜索、操作菜单和分类](docs/demo.gif)

![阅读队列、从 Chrome 保存的论文和三套主题](docs/queue.gif)

Zotero 在后台等你点浏览器按钮时，要占用将近 1 GB 内存。有了 zotty，你可以把它关掉：

- **Zotero 关着也能保存。** 一个小型接收器代替 Zotero 响应 Chrome 里的 Zotero Connector，把论文和 PDF 先放进收件箱，等 Zotero 下次打开时再交给它。
- **全屏界面。** 边输入边搜索，浏览分类，查看每篇论文的首页和摘要，打开 PDF。也支持鼠标操作。
- **阅读队列。** 把论文标为待读、在读或已读，加标签，归入分类，按 arXiv 号或 DOI 添加论文。

zotty 从不直接写 `zotero.sqlite`，也不使用 Zotero 同步。它只读取数据库，所有修改都交给 Zotero 自己完成。Zotero 关着时做的修改会先排队，等它打开后再写入。

## 系统要求

- macOS（Apple 芯片或 Intel）
- Zotero 10 或更高版本，支持个人文库（不显示群组文库）
- Chrome 里装有 Zotero Connector，用于在 Zotero 关闭时保存论文

## 安装

```bash
brew install xin-yi6/tap/zotty
```

也可以直接下载最新版本：

```bash
curl -fsSL "https://github.com/xin-yi6/zotty/releases/latest/download/zotty_darwin_$(uname -m).tar.gz" | sudo tar -xz -C /usr/local/bin zotty
```

也可以用 Go 1.27 或更高版本安装：`go install github.com/xin-yi6/zotty@latest`。

然后启动接收器，并检查设置：

```bash
zotty service install
zotty doctor
```

`zotty doctor` 发现问题时会告诉你怎么解决。如果要修改论文，需要在 Zotero 的“设置 > 高级”里打开“允许此计算机上的其他应用程序与 Zotero 通信”。第一次修改时 Zotero 会询问一次权限。

升级时运行 `brew upgrade zotty`（或重新下载安装），再运行一次 `zotty service install`。

## 使用

运行 `zotty`。按 `?` 查看所有按键，按 `space` 打开当前论文的操作菜单，按 `:` 查看所有命令。

| 按键        | 作用                                 |
| ----------- | ------------------------------------ |
| `↑` `↓`     | 移动                                 |
| `tab`       | 切换 Library、Queue、Inbox 页面      |
| `/`         | 搜索；搜索时按 `tab` 改为搜索 PDF 正文 |
| `←` `→`     | 进出分类栏，切换队列分组             |
| `enter`     | 打开 PDF                             |
| `s`         | 切换到下一个阅读状态                 |
| `e` `+` `-` | 编辑标签，加入或移出分类             |
| `a`         | 按 arXiv 号或 DOI 添加论文           |
| `t` `m`     | 切换主题，开关动效                   |

搜索时直接输入作者名、年份或标题里的词即可，不需要特殊语法。想精确搜索时，可以用 `a:` 作者、`t:` 标题、`y:` 年份、`s:` 阅读状态、`c:` 分类，或 `#标签`。

所有功能也可以在命令行里用，加 `--json` 方便脚本调用：

```bash
zotty search maldacena 1997
zotty open 1
zotty read 1 in-progress
zotty add 1706.03762 --to "Machine learning"
```

运行 `zotty help` 查看全部命令。

## 须知

- **阅读状态**的存储方式和 [Zotero Reading List](https://github.com/Dominic-DallOsto/zotero-reading-list) 插件相同，两者看到的状态一致。不需要安装这个插件。
- **“App Background Activity”提醒**：运行 `zotty service install` 和每次升级后，macOS 会弹出这条提醒，指的就是接收器。可以在“系统设置 > 通用 > 登录项与扩展”里看到它。
- **Connector 更新**：接收器用的是 Connector 的通信协议，这不是公开接口。如果 Zotero 关着时保存失败，打开 Zotero 照常保存即可。
- **卸载**：运行 `zotty service uninstall`，然后运行 `brew uninstall zotty`（或删除 `/usr/local/bin/zotty`），再删除 `~/Library/Application Support/zotty`。

## 致谢

- [tapefox](https://github.com/Egdon/tapefox)：zotty 的界面和动效受到它简洁布局的启发。
- [Zotero Reading List](https://github.com/Dominic-DallOsto/zotero-reading-list)：阅读状态的存储方式。
- [bibtui](https://pypi.org/project/bibtui/)：`a:`、`t:` 这类字段搜索。
- 基于 [Bubble Tea](https://github.com/charmbracelet/bubbletea) 和 [Lip Gloss](https://github.com/charmbracelet/lipgloss) 构建。

zotty 是独立项目，与 Zotero 官方无关。

## 许可证

[MIT](LICENSE)
