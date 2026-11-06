# zotty

English | [简体中文](README.zh-CN.md)

Zotero in your terminal. Browse, search, and work through your reading queue
without opening Zotero, and keep saving papers from Chrome while it's closed.

![Searching, the actions menu, and collections](docs/demo.gif)

![The reading queue, papers saved from Chrome, and the three themes](docs/queue.gif)

Zotero uses close to a gigabyte of memory while it waits in the background for
the browser button. zotty lets you quit it:

- **Saves while Zotero is closed.** A small receiver answers the Zotero
  Connector in Chrome, keeps the paper and its PDF in an inbox, and hands them to
  Zotero the next time it opens.
- **A full-screen interface.** Search as you type, browse collections, see
  each paper's first page and abstract, and open PDFs. Works with the mouse too.
- **Your reading queue.** Mark papers to read, in progress, or read; add tags;
  file them in collections; add papers by arXiv ID or DOI.

zotty never writes to `zotero.sqlite` and never uses Zotero sync. It reads the
database, and every change goes through Zotero itself. Changes made while Zotero
is closed wait and are made when it opens.

## Requirements

- macOS (Apple silicon or Intel)
- Zotero 10 or later, with your personal library (group libraries aren't shown)
- The Zotero Connector in Chrome, to save while Zotero is closed

## Install

```bash
brew install xin-yi6/tap/zotty
```

Or download the latest release:

```bash
curl -fsSL "https://github.com/xin-yi6/zotty/releases/latest/download/zotty_darwin_$(uname -m).tar.gz" | sudo tar -xz -C /usr/local/bin zotty
```

Or, with Go 1.27 or later, `go install github.com/xin-yi6/zotty@latest`.

Then start the receiver and check the setup:

```bash
zotty service install
zotty doctor
```

`zotty doctor` says how to fix anything that isn't right. To change papers,
turn on "Allow other applications on this computer to communicate with Zotero"
in Zotero's Settings > Advanced; the first change asks for permission once.

To upgrade, run `brew upgrade zotty` (or install the new release), then
`zotty service install` again.

## Use

Run `zotty`. Press `?` for every key, `space` for the selected paper's actions,
and `:` for every command.

| Key         | Action                                          |
| ----------- | ----------------------------------------------- |
| `↑` `↓`     | Move                                            |
| `tab`       | Library, Queue, Inbox                           |
| `/`         | Search; `tab` while searching looks inside PDFs |
| `←` `→`     | Collections, and queue groups                   |
| `enter`     | Open the PDF                                    |
| `s`         | Next reading state                              |
| `e` `+` `-` | Tags, add to or remove from a collection        |
| `a`         | Add a paper by arXiv ID or DOI                  |
| `t` `m`     | Theme, animation on or off                      |

Search understands names, years, and title words without any syntax. To be
exact, use `a:` author, `t:` title, `y:` year, `s:` state, `c:` collection, or
`#tag`.

Everything also works from the command line, with `--json` for scripts:

```bash
zotty search maldacena 1997
zotty open 1
zotty read 1 in-progress
zotty add 1706.03762 --to "Machine learning"
```

Run `zotty help` for all commands.

## Good to know

- **Reading states** are stored the way the
  [Zotero Reading List](https://github.com/Dominic-DallOsto/zotero-reading-list)
  plugin stores them, so both see the same states. The plugin isn't needed.
- **"App Background Activity"**: macOS shows this notice after
  `zotty service install` and after upgrades. That's the receiver; it's listed
  in System Settings > General > Login Items & Extensions.
- **Connector updates**: the receiver speaks the Connector's protocol, which
  isn't a public API. If saving ever fails with Zotero closed, open Zotero and
  save as usual.
- **Uninstall**: `zotty service uninstall`, then `brew uninstall zotty` (or
  delete `/usr/local/bin/zotty`) and `~/Library/Application Support/zotty`.

## Acknowledgements

- [tapefox](https://github.com/Egdon/tapefox), whose calm layout and motion
  inspired zotty's look.
- [Zotero Reading List](https://github.com/Dominic-DallOsto/zotero-reading-list),
  for the reading states.
- [bibtui](https://pypi.org/project/bibtui/), for field search like `a:` and `t:`.
- Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea) and
  [Lip Gloss](https://github.com/charmbracelet/lipgloss).

zotty is an independent project, not affiliated with Zotero.

## License

[MIT](LICENSE)
