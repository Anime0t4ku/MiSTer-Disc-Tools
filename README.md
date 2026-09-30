# Disc Tools

Disc Tools is a native MiSTer utility for ripping and burning physical CDs directly from your MiSTer.

It is designed for game discs, mixed-mode discs with CD audio, CHD images, and MSU1 / MD+ data discs. Disc Tools runs directly on MiSTer using the framebuffer and controller, so no desktop environment is required.

Controller input uses MiSTer's system-wide controller mappings when available, so pads configured through MiSTer use the same logical D-pad and A/B layout in Disc Tools. The direct Linux input handling remains available as a fallback when no MiSTer map exists.

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

**FAST CHD** creates the same CHD (subchannel included) with zlib compression only, skipping LZMA and FLAC. On MiSTer it is about 4 times quicker to create and quicker to decompress while playing; the file is about 15% larger for data tracks, more for discs with CD audio tracks. The BIN/CUE is removed after the CHD passes verification, like the normal CHD option.

**ULTRA FAST CHD - UNCOMPRESSED** stores the image in a CHD without any compression: one file with the data and the subchannel instead of BIN + CUE + `.sub`, about the size of the BIN, created in about the time of a file copy. `chdman verify` cannot check an uncompressed CHD, so Disc Tools compares every frame of the CHD (data and subchannel) with the rip before it removes the BIN.

Disc Tools reads the disc in raw DAO mode so mixed-mode game discs and discs containing CDDA audio are preserved correctly.

The raw subchannel (P-W) is read as well. It holds the position data (Q) of every sector, including the deliberately damaged Q sectors that some copy protections rely on, such as PSX LibCrypt. After the rip Disc Tools:

- measures and corrects the constant subchannel offset that many drives have (the subchannel of a neighbouring sector is returned);
- reads every suspicious sector again from the disc, several times: raw subchannel is not error corrected by the drive, and some drives return a whole block of sectors with the subchannel of the neighbouring sector. A sector keeps a broken Q only when the reads agree on it (copy protection or a real disc defect);
- keeps the subchannel inside the CHD;
- writes a CloneCD style `.sub` file next to the CUE/BIN (the CUE format cannot hold subchannel data);
- writes a `.subq.log` report with the sectors whose Q CRC is broken (on a LibCrypt disc these are the protection sectors).
- on a PSX LibCrypt disc, writes a `.sbi` file next to the image with the modified LibCrypt sectors (only the 64 known LibCrypt sectors are checked, and a pair is written only when both of its sectors are broken, so disc defects never end up in it). It is useful for emulators that read `.sbi` files. If a read error makes the two copies of the LibCrypt key on the disc disagree, no `.sbi` is written.
- on a PSX LibCrypt disc, also adds that `.sbi` to the Main's `sbi.zip` (see *PSX LibCrypt and burned copies* below).

If the drive cannot return raw subchannel data, the disc is ripped without it, as before.

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

If a used CD-RW is inserted, Disc Tools can perform a fast erase and continue
with the selected burn automatically. Blank CD-R and CD-RW media use the same
burning workflow.

### Erase a CD-RW

Choose **Erase CD-RW** from the main menu to reuse rewritable media. **Fast
Erase** prepares the disc for another burn and is the recommended default.
**Full Erase** erases the complete medium and takes considerably longer.

Disc Tools verifies that the inserted medium is a CD-RW before erasing it and
checks that the drive reports it as blank afterwards. Erasing cannot be safely
cancelled once the drive has accepted the command, so the progress screen stays
active until the drive finishes.

### Burn a CHD image

Choose **Burn Disc → CHD Image** and select the CHD file.

Disc Tools will:

1. Show the CHD performance warning.
2. Ask for the burn speed.
3. Extract the CHD to temporary BIN/CUE files.
4. Burn the extracted image with `cdrdao`.
5. Remove the temporary extraction files when finished.

Because CHD extraction happens on MiSTer's ARM CPU, this can be much slower than extracting the same image on a desktop PC.

### PSX LibCrypt and burned copies

A CD-R cannot carry the modified subchannel of a PSX LibCrypt disc, so a burned copy boots only if the MiSTer Main finds the game's `.sbi` in `sbi.zip`, named after the game ID (for example `SLES-02083.sbi`). Disc Tools adds it there automatically:

- when it rips a LibCrypt disc;
- when it burns a LibCrypt image. The `.sbi` comes from an `.sbi` file next to the image (same name), or else from the subchannel: the `.sub` next to a CUE, or the subcode inside a CHD. For a CHD only the two small LibCrypt areas are read, not the whole disc.

`sbi.zip` is updated in the same PSX folder the Main uses: the first of `/media/usb0..5/PSX`, `/media/usb0..5/games/PSX`, network, cifs, `/media/fat/PSX`, `/media/fat/games/PSX` that exists. It is created if missing. An `.sbi` already in `sbi.zip` for the same game ID is never replaced. The burn and rip messages say what was done.

#### Raw burn with the LibCrypt subchannel

When the image has the complete subchannel of the LibCrypt sectors (the `.sub` next to a CUE, or a CHD made with the subchannel), Disc Tools asks how to burn it:

- **STANDARD BURN (+ SBI.ZIP)**: as above, the copy needs `sbi.zip`;
- **RAW BURN WITH LIBCRYPT SUBCHANNEL**: the disc is written in raw mode (96 byte P-W, like CloneCD's RAW-DAO 96) and the modified Q of the LibCrypt sectors is written on the CD-R, so the copy carries its own protection. Only the Q of the LibCrypt sectors is taken from the image; every other sector gets the Q generated by cdrdao, so read errors of the original disc are not copied. The `.sbi` is still added to `sbi.zip`.

The raw burn needs a drive that supports raw 96 byte subchannel writing and writes the Q as sent; if the drive refuses the mode, cdrdao stops before writing and the standard burn can be used instead. It uses cdrdao's `generic-mmc-raw` driver with a Disc Tools option (`patches/cdrdao-raw-q-from-image.patch`), since cdrdao normally regenerates the whole Q channel. It supports single-BIN images whose first track is the data track.

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

- **cdrdao 1.2.6** — GPL-2.0-or-later. Used for raw DAO CD ripping, CUE/TOC handling, and physical CD writing. A small local patch (`patches/cdrdao-rw-raw-subchannel-scan.patch`) keeps pre-gap/index detection working while raw subchannel data is read.
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
