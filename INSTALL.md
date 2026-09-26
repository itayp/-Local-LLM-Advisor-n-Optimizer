# Installing Local LLM Advisor

Local LLM Advisor looks at your computer, tells you which AI models it can
run well, and lets you try them — no terminal, no config file, nothing to
type. This page is the download-to-running-app path for each system it
supports. Pick your operating system below.

Every download comes from one place: the
[Releases page](https://github.com/itayp/-Local-LLM-Advisor-n-Optimizer/releases/latest).
That page always has the newest version at the top, with one file per
system — the sections below say which file is yours and what to do with
it.

Once it's running, look for the small icon Local LLM Advisor adds (a
tray icon on macOS and Windows, similar on Linux — each section below
says exactly where). Clicking it opens the app in your browser, whenever
you want it; you don't need to reopen the file you downloaded again.

---

## macOS

1. Open the [Releases page](https://github.com/itayp/-Local-LLM-Advisor-n-Optimizer/releases/latest)
   and download **Local LLM Advisor.dmg**. One file works on every Mac —
   Apple Silicon or Intel — so there's nothing to choose.
2. Open the downloaded file. A window appears with the Local LLM Advisor
   icon and a shortcut to your Applications folder — drag the icon onto
   the shortcut.
3. Open your **Applications** folder and double-click **Local LLM
   Advisor**.

**About the security warning.** This build isn't signed with an Apple
Developer ID yet (see the note at the end of this page), so the first
time you open it, macOS's Gatekeeper will refuse with something like
*"Local LLM Advisor" can't be opened because Apple cannot check it for
malicious software.* This is expected — here's the one-time way past it:

1. In your Applications folder, **right-click** (or Control-click) **Local
   LLM Advisor** and choose **Open** from the menu — don't just double-click.
2. A dialog appears with an **Open** button this time (the right-click
   path shows it; a plain double-click doesn't). Click **Open**.
3. That's it — macOS remembers this choice, and every launch after this
   one is a plain double-click.

Local LLM Advisor has no Dock icon and no menu bar at the top of the
screen — it lives entirely in the **menu bar icon at the top right of
your screen** (the same row as your Wi-Fi and clock). Click it for a menu
with **Open Local LLM Advisor**, a **Start at login** checkbox (off until
you turn it on), and **Quit**.

**To uninstall:** quit it from the tray menu, then drag **Local LLM
Advisor** from your Applications folder to the Trash — the normal way to
remove any Mac app. If you had turned "Start at login" on, that setting is
removed with it. Your settings, downloaded models, and benchmark history
live separately, in *~/Library/Application Support/Advisor* — the
uninstall above leaves that folder alone, so reinstalling later picks up
where you left off; delete that folder yourself if you want a completely
clean slate.

---

## Windows

1. Open the [Releases page](https://github.com/itayp/-Local-LLM-Advisor-n-Optimizer/releases/latest)
   and download **LocalLLMAdvisor-Setup-\<version\>.exe**.
2. Double-click the downloaded file to start the installer.

**About the security warning.** This build isn't signed with a code-
signing certificate yet (see the note at the end of this page), so
Windows SmartScreen will likely show *"Windows protected your PC"* with a
blue **Don't run** button and no obvious way to continue. Here's the
one-time way past it:

1. Click **More info** — a **Run anyway** button appears underneath the
   warning.
2. Click **Run anyway**. This is the one screen this warning ever shows
   for this particular file (already-downloaded, already-scanned) — it
   won't reappear for the same installer.

3. The installer asks for permission to make changes to your computer
   (this is normal for anything that installs into Program Files) — allow
   it.
4. Follow the wizard: it offers a desktop shortcut and a **"Start Local
   LLM Advisor automatically when I log in"** checkbox — both off unless
   you check them. On the last page, leave **"Launch Local LLM Advisor"**
   checked to start it immediately, or uncheck it to start it later from
   the Start Menu.

Local LLM Advisor lives in your **system tray**, the icon area at the
bottom right of your screen near the clock — click the small **^** arrow
there if you don't see it right away, since Windows hides infrequently-used
tray icons by default. Right-click the icon for **Open Local LLM
Advisor**, **Start at login**, and **Quit**.

**To uninstall:** open **Settings → Apps → Installed apps**, find **Local
LLM Advisor**, and choose **Uninstall** — or use **Local LLM Advisor** in
your Start Menu, which the installer also adds an uninstaller shortcut
to. Either way removes the "start at login" entry along with everything
else the installer added. Your settings, downloaded models, and benchmark
history live separately, in *%LOCALAPPDATA%\Advisor* — uninstalling
leaves that folder alone, so reinstalling later picks up where you left
off; delete that folder yourself if you want a completely clean slate.

---

## Linux

Two ways to install, depending on what you'd rather do — both need no
terminal.

### Option A: the .deb (Debian, Ubuntu, Linux Mint, and similar)

1. Open the [Releases page](https://github.com/itayp/-Local-LLM-Advisor-n-Optimizer/releases/latest)
   and download **local-llm-advisor_\<version\>_amd64.deb**.
2. Double-click the downloaded file. It opens in your system's software
   installer (GNOME Software, Discover, or similar) — click **Install**
   and enter your password when it asks.
3. Local LLM Advisor now appears in your applications menu like any other
   app.

### Option B: the AppImage (any distribution, nothing installed system-wide)

1. Open the [Releases page](https://github.com/itayp/-Local-LLM-Advisor-n-Optimizer/releases/latest)
   and download **LocalLLMAdvisor-\<version\>-x86_64.AppImage**.
2. Right-click the downloaded file, open **Properties → Permissions**, and
   turn on **"Allow executing file as program"** (exact wording varies by
   file manager) — this is AppImage's usual one-time step, not something
   specific to Local LLM Advisor.
3. Double-click it to run. There's nothing to install and nothing to
   uninstall later beyond deleting the file — it runs entirely from
   wherever you keep it.

Neither path needs signing the way macOS and Windows do, so there's no
security warning to click through on Linux.

**Finding the tray icon.** On desktops that support tray icons out of the
box (KDE Plasma, XFCE, and others), Local LLM Advisor's icon appears in
your system tray with **Open**, **Start at login**, and **Quit**, the same
as the other two systems. **On GNOME (the default desktop on plain Ubuntu,
Fedora Workstation, and others), tray icons don't show up at all without
an extension** — this is a long-standing GNOME limitation that applies to
every tray app, not something Local LLM Advisor can work around. Install
your desktop's extension for this (search your Software app or GNOME's
extension site for "AppIndicator" or "KStatusNotifierItem Support") if
you want the icon; without it, the app is still running — reopen it any
time from your applications menu, or open `http://127.0.0.1:27182` in a
browser directly.

**Start at login** (from the tray menu, once you can see it) writes a
user systemd service — no `sudo`, nothing system-wide, and nothing turned
on until you click it yourself.

**To uninstall:** if you used the .deb, remove **Local LLM Advisor** the
same way you'd remove any other app — through your software installer, or
`apt remove local-llm-advisor` if you're comfortable with a terminal
(never required, just available). If you used the AppImage, delete the
file. Either way, if you'd turned "Start at login" on, turn it off from
the tray menu first — an uninstall doesn't reach into your systemd user
directory to undo that for you. Your settings, downloaded models, and
benchmark history live separately, in `~/.local/share/advisor` (or
wherever `$XDG_DATA_HOME` points); removing the app leaves that folder
alone, so reinstalling later picks up where you left off — delete it
yourself if you want a completely clean slate.

---

## A note on the security warnings

The Gatekeeper and SmartScreen warnings above are real, and they exist for
a good reason: signing costs money ($99/year for the Apple Developer
Program, plus a separate ongoing cost for a Windows code-signing
certificate or service), and until that's in place, this is genuinely
what an unsigned build looks like on each system. Every download still
comes straight from this project's own GitHub Releases page — the same
result-of-the-real-source-code as a signed build. If you'd rather not
click through the warning, that's a completely reasonable choice; check
the Releases page later, since the first thing that changes when signing
is turned on is that these warnings disappear, with no other change to
how the app works.

## Checking for a newer version

Local LLM Advisor never updates itself — the **Settings** screen has a
**Check for updates** button that compares your version against the
latest release and links you to this same Releases page if a newer one
exists. Nothing is downloaded or installed until you click that link
yourself.
