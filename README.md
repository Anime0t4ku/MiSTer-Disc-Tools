# Disc Tools

Disc Tools is a native MiSTer utility for ripping and burning physical CDs directly from your MiSTer.

It is designed for game discs, mixed-mode discs with CD audio, CHD images, and MSU1 / MD+ data discs. Disc Tools runs directly on MiSTer using the framebuffer and controller, so no desktop environment is required.

## Important: CHD processing on MiSTer is slow

> **CHD creation and extraction can take a long time on MiSTer.**
>
> MiSTer's ARM CPU is not particularly fast at CHD compression or decompression. For large discs, creating or extracting a CHD may take considerably longer than ripping or burning the disc itself.
>
> If speed matters, consider doing CHD conversion on a PC with **MiSTer Companion** instead.

Disc Tools shows this warning before starting CHD-heavy operations and lets you continue or cancel.

## What Disc Tools can do

### Rip a physical disc

Choose **Rip Physical Disc** from the main menu to create an image from the disc in `/dev/sr0`.

You can keep the rip as **BIN/CUE**, convert it to **CHD**, or create a CHD and remove the BIN/CUE afterwards.

Disc Tools reads the disc in raw DAO mode so mixed-mode game discs and discs containing CDDA audio are preserved correctly.

When CHD conversion is selected, the original rip is only removed **after the new CHD passes verification**. If conversion or verification fails, the BIN/CUE files are kept.

### Burn a BIN/CUE image

Choose **Burn Disc → CUE/BIN Image**, select the CUE file, then select the desired write speed.

Available speeds are:

- Auto
- 4x
- 8x
- 16x
- Maximum

Disc Tools uses `cdrdao` for the physical write.

### Burn a CHD image

Choose **Burn Disc → CHD Image** and select the CHD file.

Disc Tools will:

1. Show the CHD performance warning.
2. Ask for the burn speed.
3. Extract the CHD to temporary BIN/CUE files.
4. Burn the extracted image with `cdrdao`.
5. Remove the temporary extraction files when finished.

Because CHD extraction happens on MiSTer's ARM CPU, this can be much slower than extracting the same image on a desktop PC.

### Burn an MSU1 / MD+ data disc

Choose **Burn Disc → MSU1 / MD+ Data Disc** and select the game folder.

Disc Tools will ask for the burn speed first, then prepare the folder and build an **ISO 9660 + Joliet** data image.

The original game folder is never modified. Disc Tools creates a temporary staging copy and performs any required filename adjustments there.

For long Joliet filenames:

- MSU1 sets are renamed together so the ROM, `.msu`, and `.pcm` basename relationship stays intact.
- MD+ CUE `FILE` references are updated when referenced filenames need to be shortened.
- Filename changes are shown before the image is built.

`xorriso` is used to create the ISO9660/Joliet image. The resulting temporary ISO is then burned with `cdrdao`, which is the component that communicates with the optical drive.

## Main menu

The main menu contains:

- **Rip Physical Disc**
- **Burn Disc**
- **Eject Disc**
- **Exit**

The dedicated **Exit** item is the only way to leave Disc Tools from the main menu. Pressing B/ESC on the main menu does not exit the application.

## Controls

- **D-Pad / Arrow keys** — Navigate
- **A / Enter** — Select
- **B / Esc** — Back or Cancel

During long operations, the progress screen remains responsive and B/ESC can be used to cancel where supported.

## Where ripped files are saved

When ripping a disc, Disc Tools lets you choose the destination storage. Output can be written to the MiSTer SD card or attached USB storage.

Temporary working files, extracted CHD data, data-disc staging files, and temporary ISO images are kept inside Disc Tools' own temporary directory and cleaned up when appropriate.

## Files installed on MiSTer

Disc Tools keeps its runtime files together under `.config/disctools`:

```text
/media/fat/Scripts/disctools.sh
/media/fat/Scripts/.config/disctools/
    disctools
    bin/
        cdrdao
        cue2toc
        toc2cue
        chdman
        xorriso
    licenses/
    logs/
    temp/
```

The launcher script is the only Disc Tools file outside `.config/disctools`.

## Log file

If something goes wrong, Disc Tools writes helper output to:

```text
/media/fat/Scripts/.config/disctools/logs/disctools.log
```

This includes output from `cdrdao`, `chdman`, and `xorriso` and is useful when reporting a failed rip, conversion, or burn.

## Building for MiSTer ARMv7

Disc Tools can be built with the ARMv7 GCC toolchain used for MiSTer software:

```bash
cd "/path/to/DiscTools"
sed -i 's/\r$//' build-mister.sh build_cdrdao.sh build_xorriso.sh fetch_chdman.sh Scripts/disctools.sh
chmod +x build-mister.sh build_cdrdao.sh build_xorriso.sh fetch_chdman.sh Scripts/disctools.sh
./build-mister.sh
```

The completed MiSTer payload is placed under `Scripts/`.

## Third-party tools and licensing

Disc Tools uses separate command-line utilities for disc imaging and burning rather than linking their code into the application.

- **cdrdao 1.2.6** — GPL-2.0-or-later. Used for raw DAO CD ripping, CUE/TOC handling, and physical CD writing.
- **chdman** — MAME CHD tool. Used for CHD creation, verification, and extraction.
- **GNU xorriso 1.5.6.pl02** — GPL-3.0-or-later. Used to create ISO 9660/Joliet images for MSU1 / MD+ data discs.

The build places the ARMv7 helper binaries in `Scripts/.config/disctools/bin/`. License notices and GPL texts are installed under `Scripts/.config/disctools/licenses/`.

## Safety notes

Disc Tools is deliberately conservative with user data:

- Ripped BIN/CUE files are not deleted unless CHD creation succeeds and the CHD passes verification.
- Failed or cancelled CHD operations keep the source files.
- MSU1 / MD+ filename changes happen only inside a temporary staging copy.
- Original MSU1 / MD+ folders are never renamed or modified.
- Temporary CHD extraction and data-disc preparation files are kept inside Disc Tools' own working directory.
