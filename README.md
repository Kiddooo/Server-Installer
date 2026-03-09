# FTB Server Installer

A command-line tool for installing and updating modded Minecraft server packs. Supports modpacks from **FTB (Feed The Beast)**, **CurseForge**, and **Modrinth**.

## Features

- Install server modpacks from FTB, CurseForge, or Modrinth with a single command
- Automatic modloader installation (Forge, NeoForge, Fabric)
- Bundled Java JRE download (via Adoptium)
- Concurrent multi-threaded file downloads with retry and mirror failover
- Update detection with incremental file downloads (only changed/new files are re-downloaded)
- File integrity validation via SHA1/SHA256 checksums
- Non-interactive mode for automated/CI deployments

## Usage

This usage guide assumes that the server installer is named `serverinstaller.exe`. The installer downloaded may have a different name such as `ftb-server-windows-amd64.exe` or `serverinstaller_<pack_id>_<version_id>.exe`.

### Windows

You can either double-click on the installer to run it, or you can run it from the command line.

To run from the command line, open a command prompt and navigate to the directory where the installer is located. You can then run the installer with the following command:

```cmd
.\serverinstaller.exe -pack <pack_id> -version <version_id>
```

### MacOS/Linux

Open up a terminal and navigate to the directory where the installer is located. You can then run the installer with the following command:

```bash
./serverinstaller -pack <pack_id> -version <version_id>
```

### Provider Examples

#### FTB (default)

```bash
# Install an FTB modpack (provider defaults to ftb)
./serverinstaller -pack 129 -version 100207

# Explicitly specify the FTB provider
./serverinstaller -provider ftb -pack 129 -version 100207
```

#### CurseForge

CurseForge requires an API key. You can get one at [console.curseforge.com](https://console.curseforge.com).

```bash
# Using the -apikey flag
./serverinstaller -provider curseforge -pack 925200 -version 7722629 -apikey YOUR_CF_API_KEY

# Using an environment variable
export CURSEFORGE_API_KEY=YOUR_CF_API_KEY
./serverinstaller -provider curseforge -pack 925200 -version 7722629
```

The **pack ID** is the numeric CurseForge project ID, visible in the URL of the modpack's CurseForge page (e.g. `curseforge.com/minecraft/modpacks/all-the-mods-10/files` → project ID (e.g. `925200`) is in the page metadata, or use the API).

The **version ID** is the numeric file ID for the specific modpack version. You can find it by clicking on a file on the Files tab — the number at the end of the URL is the file ID (e.g. `.../files/7722629` → version ID `7722629`). Leave `-version` unset to install the latest release.

CurseForge server packs are supported in three formats, detected automatically:
- **ServerStarter** — packs with a `server-setup-config.yaml` (e.g. Enigmatica 8)
- **ServerPackCreator** — packs with a `manifest.json` or `variables.txt` from ServerPackCreator (e.g. Farmopolis)
- **Direct** — packs where the zip contains the server files directly or a Forge/NeoForge installer jar

#### Modrinth

```bash
# Using a project slug
./serverinstaller -provider modrinth -pack fabulously-optimized -version KWduSJZ4

# Using a project ID
./serverinstaller -provider modrinth -pack 1KVo5zza -version KWduSJZ4

# Install the latest version
./serverinstaller -provider modrinth -pack fabulously-optimized -latest
```

Modrinth supports both project slugs (e.g., `fabulously-optimized`) and alphanumeric project IDs (e.g., `1KVo5zza`). The version ID is the Modrinth version ID (visible on the version page). Only **modpack** projects are supported — passing a mod project ID will produce a clear error.

### Updating

Re-running the installer in an existing install directory with a newer `-version` (or `-latest`) will perform an incremental update:

- Only changed or new files are downloaded
- Files removed from the new version are deleted
- Unchanged files are skipped
- The modloader is re-installed if its version changed

```bash
# Update to a specific new version
./serverinstaller -provider curseforge -pack 925200 -version 7722629 -apikey $CF_KEY -dir ./my-server

# Update to the latest available version
./serverinstaller -provider ftb -pack 129 -latest -dir ./my-server
```

### Flags

| Flag              | Default              | Description                                                                                                         |
|-------------------|----------------------|---------------------------------------------------------------------------------------------------------------------|
| `-provider`       | `ftb`                | Sets the modpack provider (`ftb`, `curseforge`, or `modrinth`)                                                      |
| `-pack`           |                      | The ID of the modpack (numeric for FTB/CurseForge, slug or ID for Modrinth)                                        |
| `-version`        |                      | ID of the modpack version to install; if not set, latest stable release will be selected                            |
| `-dir`            | `./`                 | Directory to install the server files in (defaults to current directory)                                            |
| `-auto`           | `false`              | Non-interactive mode: doesn't ask questions, just runs the installer                                                |
| `-latest`         | `false`              | If the version ID is not set, gets the latest version (stable, beta, or alpha)                                     |
| `-validate`       | `false`              | Validates modpack file checksums after download and installation                                                    |
| `-force`          | `false`              | Only works with `-auto`; forces the installer to continue past warnings                                             |
| `-threads`        | `CPU cores * 2`      | Number of concurrent download threads                                                                               |
| `-apikey`         | `public`             | API key for the selected provider (FTB private packs, CurseForge API key)                                          |
| `-skip-modloader` | `false`              | Skips running the modloader installer (Forge/NeoForge/Fabric)                                                       |
| `-no-java`        | `false`              | Skips downloading a bundled copy of Java                                                                            |
| `-just-files`     | `false`              | Only downloads modpack files; skips Java and modloader installation                                                 |
| `-no-colours`     | `false`              | Removes colour formatting from console output                                                                       |
| `-timeout`        | `120`                | File download timeout in seconds                                                                                    |
| `-accept-eula`    | `false`              | Accepts the Minecraft EULA automatically ([EULA](https://account.mojang.com/documents/minecraft_eula))             |
| `-verbose`        | `false`              | Enables debug logging                                                                                               |

### Environment Variables

| Variable               | Description                                                      |
|------------------------|------------------------------------------------------------------|
| `FTB_MODPACK_API_KEY`  | API key for private FTB modpacks (alternative to `-apikey` flag) |
| `CURSEFORGE_API_KEY`   | CurseForge API key (alternative to `-apikey` flag)               |

### Automated / CI Usage

For automated deployments (Docker, CI pipelines, etc.), use the `-auto` flag combined with `-force`:

```bash
# Automated FTB server install
./serverinstaller -provider ftb -pack 129 -version 100207 -auto -force -accept-eula

# Automated CurseForge server install
./serverinstaller -provider curseforge -pack 925200 -version 7722629 -apikey $CF_KEY -auto -force -accept-eula

# Automated Modrinth server install
./serverinstaller -provider modrinth -pack fabulously-optimized -latest -auto -force -accept-eula
```