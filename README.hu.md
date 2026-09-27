# Magical Sandboxes 0.1.0

Az `msbx` macOS-en futó parancssori eszköz, amely Debian ARM64 virtuális gépben indít shellt vagy AI harness-t. A vendég a kiválasztott projektet írható VirtioFS megosztásként kapja meg; a guest Linux lemeze futások között megmarad.

Részletes rendszerleírás: [docs/magical-sandboxes.hu.md](docs/magical-sandboxes.hu.md). Harnessenkénti működés és hibakeresés: [Codex](docs/codex.hu.md), [Claude Code](docs/claude.hu.md), [OpenCode](docs/opencode.hu.md).

## Követelmények

- macOS 14 vagy újabb, Apple Silicon (`arm64`)
- Xcode Command Line Tools és Swift 5.9 vagy újabb
- Go 1.23 vagy újabb
- Hálózati hozzáférés a Debian image, a guest csomagok és a harness telepítők letöltéséhez

A VM helper az Apple Virtualization.framework-et használja. Minden harness a saját natív belépését és beállításait használja az adott projekt tartós guest VM-jében. Nincs hoston kezelt harness-belépés vagy credential-továbbítás.

## Nulláról történő felépítés

A parancsokat a `magical-sandboxes` repository gyökeréből futtasd.

### 1. Fordítsd le a host és guest programokat

```sh
./scripts/bootstrap.sh
./bin/msbx doctor
```

A bootstrap lefordítja és Virtualization entitlementtel aláírja a `bin/msbx-vm` VM helpert. Go használatával lefordítja a `bin/msbx` és `bin/msbx-agent-linux-arm64` programokat is. A `doctor` ellenőrzi, hogy a host megfelel-e a VM indításához szükséges követelményeknek.

### 2. Készítsd elő és provisionáld a guest VM-et

```sh
./scripts/prepare-cloud-guest.sh
./scripts/provision.sh
```

Az előkészítő letölti és checksum alapján ellenőrzi a Debian 13 ARM64 cloud image-et, elkészíti a guest diszket és a cloud-init seedet. A provisioning a vendégbe telepíti a Docker Engine-t, a Codexet, a Claude Code-ot, az OpenCode-ot és az `msbx-agent` szolgáltatást. A folyamat a soros konzolon jelzi az előrehaladást, és sikertelen telepítésnél leáll.

A tiszta, provisionált guest sablon — a diszk, cloud-init seed, EFI-állapot és provisioning napló — itt található:

```text
.msbx-dev/sandboxes/template/
```

Ez a sablon a projektenkénti VM-lemezek forrása; közvetlenül nem harness-futtatásra szolgál.

### 3. Jelentkezz be az egyes projektek VM-jében

Indítsd el a harness-t a célprojektből. Első alkalommal használd a harness szokásos interaktív belépési folyamatát. Minden projekt saját guest HOME-ot és belépési adatokat kap; másik projektben külön kell bejelentkezni.

```sh
./bin/msbx run codex
./bin/msbx run claude
./bin/msbx run opencode
```

## Shell vagy harness indítása

A `shell` és a `run` parancsokat mindig egy, a `magical-sandboxes` repository-tól különálló projekt könyvtárából indítsd. Az msbx megtagadja az indítást, ha a megosztandó útvonal átfed az msbx telepítési könyvtárával.

```sh
mkdir -p ~/Work/msbx-test-project
cd ~/Work/msbx-test-project

/útvonal/a/magical-sandboxes/bin/msbx init
/útvonal/a/magical-sandboxes/bin/msbx status
/útvonal/a/magical-sandboxes/bin/msbx shell
/útvonal/a/magical-sandboxes/bin/msbx run codex
/útvonal/a/magical-sandboxes/bin/msbx run claude
/útvonal/a/magical-sandboxes/bin/msbx run opencode
```

A `run` után megadhatók az adott harness saját argumentumai is:

```sh
/útvonal/a/magical-sandboxes/bin/msbx run codex --help
```

Az `msbx status` megmutatja, hogy az adott projekt VM-je fut, indul vagy leállt-e. Kiírja a sandbox inicializáltságát, a guest lemez és EFI-állapot méretét, a sablon készültségét és a menedzsernapló utolsó módosítását is. A státuszlekérdezés nem indítja el a VM-et; a guest lemez zárolását ellenőrzi.

### VM-méret beállítása projektenként

Az alapméret a Small: 2 vCPU és 4096 MiB memória. Felülíráshoz hozz létre `.env` fájlt a célprojekt könyvtárában, és állítsd be benne a `MSBX_VM_CPUS` és/vagy `MSBX_VM_MEMORY_MIB` változót. A repository `.env.example` fájlja kommentelt példaként felsorolja a Micro, Small, Medium és Large méreteket. Egyszerű `KEY=VALUE` sorokat használ; a fájl tartalmát a program nem hajtja végre shellkódként. A folyamat környezeti változói elsőbbséget élveznek a `.env` értékeivel szemben. A módosítás a projekt VM következő indulásakor érvényesül; a már futó VM méretét nem változtatja meg.

```dotenv
MSBX_VM_CPUS=2
MSBX_VM_MEMORY_MIB=4096
```

A guest shellben a felhasználó `msbx`, amelynek nincs sudo hozzáférése, de tagja a `docker` csoportnak:

```sh
whoami
id
groups
docker version
```

Az `msbx init` az aktuális, kanonikus projektútvonalhoz saját VM-lemezt és EFI-állapotot hoz létre a `.msbx-dev/sandboxes/projects/<project-id>/` alatt. Az azonosító az útvonal SHA-256 hash-e. Az első `msbx shell` vagy `msbx run` elindítja a projekt VM-jét; az ugyanebből a projektből indított további munkamenetek csatlakoznak ehhez a VM-hez. A guest agent a munkamenetek érkezésekor bővíti a worker poolt; nincs beállított darabszámkorlát, a párhuzamosságot a VM és a Mac erőforrásai határozzák meg. Az utolsó munkamenet kilépése után a menedzser három másodpercet vár, majd leállítja a vendéget.

Külön inicializált projektek egyszerre is futhatnak, saját guest lemezzel és EFI-állapottal. Egy projekten belül a harness-ek ugyanazt a VM-et, guest HOME-ot és írható projektfájlokat használják. Minden sandbox-azonosító a projekt kanonikus útvonalához kötött. A projekt áthelyezése vagy átnevezése új azonosítót és külön sandboxot eredményez.

Az aktuális projektkönyvtárhoz tartozó VM és guest adatok végleges törléséhez előbb állítsd le annak VM-munkamenetét, majd futtasd:

```sh
/útvonal/a/magical-sandboxes/bin/msbx delete
```

A parancs megmutatja a teljes projektútvonalat, és interaktív megerősítést kér. Törli az ehhez a projekthez tartozó guest lemezt, HOME-ot, natív harness-belépéseket és más guest fájlokat. A célprojekt könyvtárában levő fájlokat nem törli. A törlés előtt minden munkamenetnek ki kell lépnie; az utolsó után a VM három másodpercen belül leáll.

## Projekt VM mentése és visszaállítása

Az `msbx backup` parancsot leállított projekt VM mellett futtasd. PAX TAR archívumot készít, Zstandard tömörítéssel és age titkosítással. A csomag tartalmazza a VM-lemezt, a guest HOME-ot, a harness-belépéseket és beállításokat, az EFI-állapotot, valamint egy ellenőrzőösszegeket és kompatibilitási adatokat tartalmazó manifestet. Alapértelmezés szerint a `.msbxbackup.tar.zst.age` mentés az aktuális projektkönyvtár `.msbx/backups/<project-id>/` mappájába kerül; argumentummal másik kimeneti útvonal adható meg. A parancs titkosítási jelszót kér. Őrizd meg biztonságosan, mert nem állítható vissza.

Visszaállításhoz az eredeti, kanonikus projektkönyvtárból futtasd az `msbx delete` parancsot, ha ott még létezik VM, majd add meg a mentést: `msbx restore /path/to/backup.msbxbackup.tar.zst.age`. A restore visszafejti és ellenőrzi a teljes archívumot, megerősítést kér, majd létrehozza a VM-et. Csak ugyanahhoz a projektútvonalhoz és kompatibilis VM-háttérhez készült mentést fogad el. A csomagformátum platformfüggetlen, de a jelenlegi VM-tartalom Apple Virtualization és ARM64 specifikus. A hostról megosztott projektfájlokat nem tartalmazza a VM-mentés. Részletek a [mentési formátumról szóló döntésben](docs/decisions/0001-portable-backup-format.md).

## Kódmódosítások újraépítése

### Swift formázás ellenőrzése

Formázd, majd ellenőrizd a Swift-kódot egyszeri Docker-konténerben; a `swift-format` nem kerül telepítésre a fejlesztői gépre:

```sh
make format-swift
make lint-swift
```

Az első futtatáskor a Docker szükség esetén letölti a `swift:6.2` image-et. A `format-swift` módosítja a Swift-fájlokat; a `lint-swift` csak ellenőrzi őket. Mindkét konténer a parancs végén törlődik.

### Csak a Go host CLI változott

Ha kizárólag a `cmd/msbx/` kódja módosult, elég ezt újrafordítani:

```sh
go build -o bin/msbx ./cmd/msbx
```

### A guest agent vagy több host komponens változott

Futtasd újra a teljes bootstrapet:

```sh
./scripts/bootstrap.sh
```

Ez újrafordítja a host Go binárisokat, a Linux ARM64 guest agentet és a Swift VM helpert, majd aláírja a VM helpert a szükséges jogosultsággal. Ha a guest agent forrása (`cmd/msbx-agent/`) változott, a bootstrap frissíti a `bin/msbx-agent-linux-arm64` binárist. A telepítéséhez építsd újra a guest sablont; a meglévő projektes VM-lemezek nem frissülnek automatikusan.

### A Swift VM helper változott

A teljes bootstrap ajánlott, mert nemcsak lefordítja, hanem alá is írja a helpert:

```sh
./scripts/bootstrap.sh
```

### Guest telepítési vagy wrapper kód változott

Ha a `scripts/prepare-cloud-guest.sh` változott, építsd újra a tiszta guest sablont:

```sh
./scripts/bootstrap.sh
rm -rf .msbx-dev/sandboxes/template
./scripts/prepare-cloud-guest.sh
./scripts/provision.sh
```

**Figyelem:** a `rm` a tiszta sablon lemezét és EFI-állapotát törli. A `.msbx-dev/sandboxes/projects/` alatti projektes VM-ek nem frissülnek a sablon újjáépítésekor; megtartják saját lemezüket, guest belépéseiket és fájljaikat. Egy projekt VM újralétrehozása törli annak guest adatait.

Guest agent- vagy telepítési változtatások projekt VM-re alkalmazásához építsd újra a fenti sablont, majd az adott projekt könyvtárából hozd létre újra a VM-et:

```sh
/útvonal/a/magical-sandboxes/bin/msbx delete
/útvonal/a/magical-sandboxes/bin/msbx init
```

Az `msbx delete` véglegesen törli a projekt guest lemezét, beleértve a natív harness belépéseket és beállításokat. A VM újrainicializálása után jelentkezz be újra. Ismételd meg minden olyan projekt VM-nél, amelyre az új guest image-et telepíteni szeretnéd.

Dokumentációs változtatás után nincs szükség újrafordításra vagy újraprovisionálásra.

## Hitelesítési adatok és állapot

Minden projekt VM-je a saját guest HOME-jában tárolja a harness-belépéseket és beállításokat. Ezek a VM leállása után is megmaradnak, más projektektől elkülönülnek, és az `msbx delete` törli őket. Titkosított VM-mentéshez használd az `msbx backup`, visszaállításhoz az `msbx restore` parancsot.

- Debian alapimage és cache: `.msbx-dev/guest/debian-13-generic-arm64-<DEBIAN_IMAGE_VERSION>/`
- Tiszta guest sablon: `.msbx-dev/sandboxes/template/`
- Projektenkénti guest diszk és EFI állapot: `.msbx-dev/sandboxes/projects/<project-id>/`
- Natív harness belépés és beállítások: az adott projekt VM-lemezén tárolt guest HOME

## Segítség és dokumentáció

```sh
./bin/msbx help
./bin/msbx version
./bin/msbx doctor
```

- [Rendszeráttekintés és VM működés](docs/magical-sandboxes.hu.md)
- [Fejlesztői útmutató és platformfüggetlenségi irány](docs/development.hu.md)
- [Codex indítás és hibakeresés](docs/codex.hu.md)
- [Claude Code indítás és hibakeresés](docs/claude.hu.md)
- [OpenCode indítás és hibakeresés](docs/opencode.hu.md)
