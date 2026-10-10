# Windows (WSL2)

On Windows, Seed runs inside **WSL2**, the Linux that ships with Windows,
with **Docker Desktop** running the Seeds' containers. Your browser stays the
Windows one.

## Set up once

1. **WSL2.** In PowerShell (as administrator): `wsl --install`, then restart.
   This installs Ubuntu; open it from the Start menu and create your user.
2. **Docker Desktop** for Windows. In its **Settings → Resources → WSL
   integration**, turn it on for your Ubuntu. Check it in Ubuntu with
   `docker info`.
3. **seed**, in the Ubuntu terminal:

   ```bash
   curl -fsSL https://raw.githubusercontent.com/paezao/seed/main/install.sh | sh
   ```

## Plant a Seed

In the Ubuntu terminal, **in your Linux home**:

```bash
cd ~
export OPENROUTER_API_KEY=sk-or-…
seed new myapp -e OPENROUTER_API_KEY
```

Your Windows browser opens on the control plane (`http://localhost:8080/_seed/`;
with `wslview` installed, or else through Explorer). If it doesn't, the
terminal prints a one-time sign-in link to open yourself.

## Keep Seeds in the Linux filesystem

Don't plant Seeds under `/mnt/c/…` (your Windows drives): a Seed's database
needs Linux file permissions that Windows drives don't keep, and Windows
drives are slow from WSL. `seed` refuses to plant or run one there.

To open a Seed's files on Windows, use VS Code with the **WSL** extension
(`code .` in the Seed's folder), or Explorer at `\\wsl$\Ubuntu\home\<you>\myapp`.

## If something's off

- **"I run in a container…"**: Docker Desktop isn't running, or its WSL
  integration is off for this distro.
- **The page doesn't load in Windows**: WSL forwards `localhost` to Windows
  by default; if you turned that off (`localhostForwarding=false` in
  `.wslconfig`), turn it back on, or open the address `seed run` printed.

Native Windows, without WSL, isn't supported.
