# A magical-sandboxes működése

Ez a dokumentum a `magical-sandboxes` host eszközeit, VM-életciklusát, guest agentjét, projektmegosztását, hitelesítését, adattárolását és üzemeltetési folyamatait foglalja össze. A Codex, Claude Code és OpenCode sajátosságairól a [Codex](codex.hu.md), [Claude Code](claude.hu.md) és [OpenCode](opencode.hu.md) dokumentáció ad részletes hibakeresési útmutatót.

## Mire való az msbx?

Az `msbx` egy macOS-en futó parancssori eszköz, amely Apple Virtualization.framework segítségével Linux ARM64 virtuális gépet indít. A guestben a felhasználó egy shellt vagy valamelyik támogatott AI harness-t használhat. A VM saját Linux diszket és Docker Engine-t kap; a munkakönyvtárként kiválasztott host projektet a rendszer írható VirtioFS megosztásként csatolja be.

Az msbx célja, hogy a harness a külön guest környezetben fusson, miközben a projektfájlokat közvetlenül a hoston szerkeszti. A harness belépési adatai és beállításai az adott projekt guest HOME-jában tárolódnak.

## Követelmények és komponensek

### Host

- macOS Apple Silicon gépen (`arm64`).
- Xcode Command Line Tools és Swift a Virtualization.framework-es helper fordításához.
- Go a `msbx` és a Linux guest agent forrásból fordításához.
- A VM helpernek tartalmaznia kell az `com.apple.security.virtualization` jogosultságot. A bootstrap aláírja és ellenőrzi ezt.
- Hálózati hozzáférés szükséges a Debian image, a guest csomagok és a harness telepítők letöltéséhez.

### Fő programok

| Fájl | Feladat |
| --- | --- |
| `bin/msbx` | Felhasználói CLI, projektútvonal ellenőrzése, VM-menedzser indítása és guest munkamenetek továbbítása. |
| `bin/msbx-vm` | Swift helper a Virtualization.framework VM konfigurálásához, indításához, terminál- és vsock-kapcsolatához. |
| `bin/msbx-agent-linux-arm64` | A guestbe telepített Linux agent forrásból fordított binárisa. |

## Telepítés és előkészítés

A repository gyökeréből futtasd:

```sh
./scripts/bootstrap.sh
./bin/msbx doctor
./scripts/prepare-cloud-guest.sh
./scripts/provision.sh
```

A `bootstrap.sh` ellenőrzi a macOS/arm64 és Xcode Command Line Tools követelményeket, lefordítja és Virtualization entitlementtel aláírja a Swift VM helpert, majd ha elérhető Go, lefordítja a host programokat és a Linux ARM64 guest agentet. A `doctor` ezeket a host oldali előfeltételeket ellenőrzi; nem telepíti a függőségeket.

A `prepare-cloud-guest.sh` letölti a Debian 13 generic ARM64 cloud image-et, SHA-512 ellenőrzést végez, majd egy 12 GiB-ra növelhető virtuális diszket és cloud-init seedet készít. A `provision.sh` ezt a diszket elindítja cloud-init konfigurációval. Ez létrehozza a guest felhasználót és az agent szolgáltatást, telepíti a Docker Engine-t, majd egymás után telepíti a Codexet, Claude Code-ot és OpenCode-ot. A hosszú telepítések időkorlátosak, a log a soros konzolra és a guest naplójába is kerül; hiba esetén a további telepítés megszakad és a VM leáll.

A tiszta guest sablon és a projektenkénti vendéglemezek a repository `.msbx-dev/sandboxes/` könyvtárában maradnak. A sablonból a projekt inicializálásakor készül másolat; a projektes lemez és EFI változótár ezután külön-külön megmarad.

## Parancsok és munkafolyamat

```text
msbx doctor
msbx version
msbx init
msbx status
msbx delete
msbx shell
msbx run codex [harness args...]
msbx run claude [harness args...]
msbx run opencode [harness args...]
```

Az `init`, `shell`, `delete` és `run` parancsokat egy külön célprojekt könyvtárából kell indítani:

```sh
cd ~/Work/my-project
/path/to/magical-sandboxes/bin/msbx init
/path/to/magical-sandboxes/bin/msbx shell
```

Az `init` szükség esetén létrehozza a projekt VM-et. Az első `run` vagy `shell` elindítja a projekt VM-menedzsert; a további munkamenetek ehhez a futó VM-hez csatlakoznak. A `run` elindítja a kiválasztott harness-t, és átadja neki a további argumentumokat. A `shell` interaktív, login Bash-t indít. A `delete` interaktív megerősítés után eltávolítja az aktuális projekthez tartozó guest lemezt és állapotot; futó VM esetén megtagadja a törlést. A harness-ek natív guest belépést és beállításokat használnak, projektenként elkülönítve.

## VM és guest életciklus

Az `msbx run`, `msbx shell`, `msbx init`, `msbx status` és `msbx delete` a futó `msbx` bináris helyéből állapítja meg a telepítési gyökeret. Az aktuális munkakönyvtárat kanonikus útvonalra oldja fel, és elutasítja, ha az átfed az msbx telepítési fájával. Így a guest számára írható megosztás nem tárhatja fel és nem módosíthatja az msbx telepítését. Az `msbx init` a kanonikus projektútvonal SHA-256 hash-ét használja azonosítóként, és az adott VM fájljait a `.msbx-dev/sandboxes/projects/<project-id>/` alatt hozza létre a `.msbx-dev/sandboxes/template/` tiszta sablon másolásával. A metadata eltárolja a teljes projektútvonalat, hogy a hash-elt VM könyvtár azonosítható legyen. Az `msbx delete` a teljes útvonal megmutatása és interaktív megerősítés után törli az aktuális projekthez tartozó inicializált VM-et; zárolt guest lemez esetén megtagadja a törlést.

A VM-menedzser a guest lemez kizárólagos, nem blokkoló zárolását a VM teljes élettartama alatt tartja. A külön projektek külön lemezt használnak és párhuzamosan futhatnak; egy projekten belüli munkamenetek ugyanahhoz a VM-menedzserhez és vendéghez csatlakoznak. A lock fájl a sandbox könyvtárban maradhat, de a tényleges zárolást az operációs rendszer tartja fenn, így egy váratlan helper-kilépés nem hagy beragadt lockot. Az inicializálás a lemez és az EFI-állapot másolása közben projektenkénti életciklus-zárolást is tart.

**Párhuzamosság:** inicializált projektek külön guest diszket, EFI-állapotot, guest HOME-ot és natív belépést kapnak. A különböző projektek VM-jei párhuzamosan futhatnak. A guest agent egy üres workert tart készenlétben, és új munkamenet érkezésekor létrehozza a következőt; nincs beállított darabszámkorlát, a tényleges párhuzamosságot a VM és a Mac erőforrásai szabják meg. Az utolsó munkamenet kilépése után a menedzser három másodpercet vár, majd leállítást kér. Ezalatt új munkamenet csatlakozhat a futó VM-hez. A projektazonosító a kanonikus útvonalból készül, ezért minden sandbox ahhoz az útvonalhoz kötődik, amelyből létrejött; áthelyezés vagy átnevezés külön sandboxot eredményez.

A VM konfigurációja jelenleg 4 CPU-t és 4096 MiB memóriát ad a guestnek, NAT hálózattal, Virtio blokkeszközzel, soros konzollal, Virtio-socketkel és egyetlen, írható VirtioFS megosztással. A megosztás címkéje `msbx-project`; a guest agent ugyanarra az abszolút útvonalra mountolja, amelyet a host küldött, és a parancsot ebben a könyvtárban indítja.

A guestben a systemd által indított `msbx-agent` rootként fut. Egy üres session workert tart fenn, és elfogadott munkamenetnél indítja a következőt; a munkamenetek igény szerint csatolják a projektet és külön PTY-t kapnak. Egy elkülönített vezérlő worker figyeli a menedzser leállítási kérését. A tényleges shell és harness parancs viszont a `msbx` Linux felhasználó UID/GID-jével fut; a provisioning ennek UID-jét a host UID-jével egyezteti, és a felhasználót a `docker` csoportba teszi. A `msbx` felhasználónak nincs sudo hozzáférése.

A helper a vendég munkamenet-folyamatait a Virtio-vsock 4050-es portján, a leállítási vezérlőfolyamatot pedig a 4052-es porton fogadja. A munkamenetprotokoll MSBX/8. A kérés a munkakönyvtárat, terminálbeállításokat, parancsot és argumentumokat tartalmazza. Minden host terminál külön guest PTY-hez csatlakozik. Host kapcsolat megszakadásakor az agent SIGHUP-ot küld a munkamenet folyamatcsoportjának. A menedzser csak az utolsó munkamenet után kéri a guest leállítását.

## Tárolás és állapot élettartama

| Hely | Élettartam és tartalom |
| --- | --- |
| `.msbx-dev/guest/` | Letöltött Debian image és az előkészítés alapfájljai. |
| `.msbx-dev/sandboxes/template/` | Tiszta, provisionált guest sablon; új projektes VM-lemezek forrása, közvetlenül nem sessionök futtatására szolgál. |
| `.msbx-dev/sandboxes/projects/<project-id>/disk.raw` | Projektenkénti, tartós Linux root diszk. Az adott projekt guest HOME-ja és sandbox-local adatai ezen maradnak meg. |
| `.msbx-dev/sandboxes/projects/<project-id>/efi-vars.fd` | Projektenkénti VM EFI változótár. |
| Host célprojekt könyvtára | A guestben RW VirtioFS-ként megosztott aktuális munkakönyvtár. |
| Guest `/home/msbx` | Projektenként tartós HOME natív harness belépésekkel és beállításokkal. |
| Guest `/run/msbx/` | Futásidejű RAM-backed fájlok; a guest leállásakor elvesznek. |

A projektes megosztás a host fájlrendszerét használja, így annak tartalma a VM leállásakor is megmarad. A guest HOME, natív belépések és beállítások a projekt virtuális lemezén tárolódnak.

## Hitelesítés és állapot

Minden harnessbe a guesten belül, natív módon jelentkezz be. Minden projekt VM-je saját, tartós guest HOME-ot és belépést kap. A projekt VM törlése az adott projekt guest belépését és beállításait is törli.

## Diagnosztika és hibakeresés

Hoston:

```sh
./bin/msbx doctor
./bin/msbx version
```

A `doctor` a macOS/arm64 környezetet, Xcode Command Line Tools-t, Swiftet, VM helper elérhetőségét és Virtualization entitlementet ellenőrzi. Go hiánya figyelmeztetés, ha a kész binárisok már rendelkezésre állnak; forrásból való bootstraphoz Go szükséges.

A guest shellben ellenőrizhető a futó felhasználó és Docker-hozzáférés:

```sh
whoami
id
groups
docker version
mountpoint /path/to/project
```

Az indulási vagy leállási hibánál először állapítsd meg, melyik réteg érintett: host CLI, VM helper, guest cloud-init/provisioning, guest agent, harness wrapper vagy projektmegosztás. Provisioning naplója a guest `/var/log/msbx-provision.log` fájlja és a VM soros konzolja; a harness wrapper hibája közvetlenül a terminálon jelenik meg. Harness-specifikus állapotellenőrzéshez használd a három dedikált harness leírást.

Gyakori okok:

- **`msbx-vm`, guest disk vagy agent hiányzik:** futtasd a bootstrap, guest előkészítő és provisioning lépéseket; a parancs kiírja, melyik útvonal hiányzik.
- **Az indítás tiltott útvonalon:** lépj ki az msbx repository könyvtárából, és egy külön célprojektben indítsd a `shell` vagy `run` parancsot.
- **Guest agent nem mountolja a projektet vagy nincs shell:** ellenőrizd a guest provisioning logot, a systemd agent szolgáltatást és a VirtioFS mountot.
- **Nincs Docker a guestben:** ellenőrizd, hogy a provisioning Docker lépése sikerült-e, illetve a guest daemon fut-e. A harness a `msbx` felhasználó csoporttagságát örökli.
- **A harness belépést kér:** jelentkezz be a harness saját guest felületén. Ha nem őrzi meg a belépést, ellenőrizd a projektútvonalat és a projekthez tartozó VM-lemezt.
- **Guest adatok eltűntek:** különítsd el a tartós guest lemezt és a `/run` ideiglenes fájljait. A guest lemez törlése a natív belépés és beállítások elvesztésével jár.

## Jelenlegi határok

- A megoldás jelenlegi host célja macOS Apple Silicon, a guest Debian 13 ARM64.
- A guest hálózati hozzáférése NAT-on keresztül történik; nincs itt projekt szintű hálózati szabályozó dokumentálva.
- A VM vezérlő agent root jogosultsággal fut, mert ő mountolja a VirtioFS projektet és kéri a leállítást. A felhasználói shell és harness folyamat nem root.
- A három harness telepítő és auth működése eltérő; ezeket külön dokumentáció tárgyalja.
- A guest munkamenet-folyamatok száma igény szerint nő; a tényleges párhuzamosságot a VM és a Mac erőforrásai korlátozzák.
