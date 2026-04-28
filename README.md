# VSMM: Vintage Story Mod Manager (Go CLI)

`vsmm` is a config-first mod manager for Vintage Story:
- Keep a JSON list of desired mods.
- Sync to ensure they are downloaded and present in your mods folder.
- Search/browse mods from the VS mod DB in CLI.
- Create and restore backups of your local mods folder.
- Configure paths from CLI (`vsmm config ...`).

## Build

```bash
cd /workspace/vintage-story-mod-manager
go build -o vsmm .
```

## Install (Recommended)

```bash
./scripts/install.sh
```

This installs `vsmm` to `~/.local/bin/vsmm` and sets up:

- `~/.vsmm/config.json`
- `~/.vsmm/cache/`
- `~/.vsmm/backups/`

No `sudo` needed.

## Quick Start

1. Initialize config (if you did not use installer):

```bash
./vsmm init
```

2. Search mods:

```bash
vsmm search quern --limit 10
```

3. Add mods:

```bash
# Track latest compatible release
vsmm add serverstatusquery

# Pin an exact release
vsmm add serverstatusquery --version 1.0.18
```

4. Sync to your game mod folder:

```bash
vsmm sync
```

5. Configure mod folder (example: Flatpak install):

```bash
vsmm config --mod-dir /home/gryph/.var/app/at.vintagestory.VintageStory/config/VintagestoryData/Mods
```

Show current config:

```bash
vsmm config
```

6. Back up mods:

```bash
vsmm backup-create
vsmm backup-list
vsmm backup-restore mods-backup-YYYYMMDD-HHMMSS.zip --clean
```

## Commands

- `init`: Create config file.
- `search`: Find mods by text.
- `show`: Show mod info + releases.
- `browse`: Interactive search + add flow.
- `add`: Add/update a mod in config.
- `list`: List configured mods.
- `remove`: Remove configured mod.
- `sync`: Download/copy missing mods to mod dir.
- `install`: Alias for `sync`.
- `config`: View or update config values (`mod_dir`, `cache_dir`, `backup_dir`, `game_version`).
- `backup-create`: Create zip backup from mod dir.
- `backup-list`: List available backups.
- `backup-restore`: Restore backup into mod dir.

## Config Schema

Default config path: `~/.vsmm/config.json`

Example:

```json
{
  "backup_dir": "~/.vsmm/backups",
  "cache_dir": "~/.vsmm/cache",
  "game_version": "1.21.6",
  "mod_dir": "~/.config/VintagestoryData/Mods",
  "mods": [
    {
      "asset_id": 27202,
      "id": "serverstatusquery",
      "moddb_id": 4575,
      "name": "Simple Server status query (now with WebCartographer integration)",
      "source_url": "https://mods.vintagestory.at/show/mod/27202",
      "strategy": "latest"
    },
    {
      "game_version": "1.21.6",
      "id": "someothermod",
      "pinned_version": "2.4.1",
      "strategy": "pinned"
    }
  ]
}
```

## Notes

- Uses the official VS mod DB API for search/mod metadata.
- Also supports resolving `show/mod/<assetid>` references through a small HTML fallback.
- `sync --prune` removes files that were previously managed by `vsmm` but are no longer in config.
# seraph-mpm
