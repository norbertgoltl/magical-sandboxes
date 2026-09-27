# Fejlesztői útmutató

Ez az útmutató rögzíti a projekt fejlesztési alapelveit és hosszú távú irányát, hogy a tervezési döntések a kódbázis növekedésével is visszakereshetők maradjanak.

## Platformfüggetlenség

### Irány

A hosszú távú cél, hogy a `magical-sandboxes` több host platformon is használható legyen. A jelenlegi megvalósítás macOS-specifikus; a projektet nem szabad platformfüggetlenként bemutatni. Egy új platform támogatásához azon a platformon is megvalósítás és ellenőrzés szükséges.

### Jelenlegi megvalósítás

- A host VM helper az Apple Virtualization keretrendszerét használja, Apple silicon célra.
- A VM-lemez előkészítése, provisioningje és életciklusa jelenleg macOS-eszközökre támaszkodik.
- A VM-mentés PAX TAR archívumot, Zstandard tömörítést és age-alapú, jelszavas titkosítást használ.
- A guest Linux ARM64. A harness-ek a guestben futnak, és a saját natív bejelentkezési, valamint beállításkezelési működésüket használják.

Ezek a jelenlegi megvalósítás részletei, nem végleges architekturális követelmények. Az új fejlesztések ne terjesszék tovább a host platformhoz kötött feltételezéseket olyan kódrészekbe, amelyek platformfüggetlenek maradhatnak.

### Új fejlesztések alapelvei

1. A központi működés maradjon független a host operációs rendszer API-jaitól, ahol ez ésszerűen megoldható.
2. Az elkerülhetetlen operációsrendszer- és virtualizációs különbségek kis, egyértelmű platformrétegekbe kerüljenek.
3. A VM életciklusa és a mentési csomag kezelése legyen különválasztva, hogy a központi backup működés ne függjön egy platform saját lemezkép-eszközétől.
4. Egy mentés attól még nem hordozható, hogy a külső archívumformátuma hordozható. A VM-lemez függhet a guest architektúrájától, a virtuális hardvertől, a lemezformátumtól vagy a hypervisortól.
5. A fejlesztés során kerüljük a host globális telepítését és a tartós hostszintű állapotot. A generált fejlesztési állapot a repositoryban vagy a célprojekt munkakönyvtárában maradjon, kivéve, ha egy változtatás kifejezetten mást kíván.
6. Minden projekt VM-jében maradjon meg az adott harness natív bejelentkezési és beállítási működése.

### A mentések hordozhatósága

A mentési csomag platformfüggetlen formátumú: a PAX TAR tárolja a VM-fájlokat és a verziózott manifestet, a Zstandard tömöríti az archívumot, az age pedig jelszóval titkosítja. A megvalósítás Go-könyvtárakat használ, nem a host lemezkép-eszközeit. A jelszót a program interaktívan kéri be, nem kerül parancssori argumentumba.

Ettől a VM-tartalom még nem válik minden platformon futtathatóvá. A jelenlegi visszaállítás ugyanazt az Apple Virtualization hátteret, ARM64 guestet, raw lemezformátumot és manifestben rögzített kanonikus projektútvonalat igényli. Egy későbbi backendhez külön meg kell határozni a tartalom kompatibilitását vagy konvertálását, mielőtt platformok közötti visszaállítást ígérnénk. A formátum és kompatibilitási szerződés részleteit a [0001-es ADR](decisions/0001-portable-backup-format.md) rögzíti.

### Platformtámogatás állítása

Mielőtt a projektet platformfüggetlenként jelölnénk, határozzuk meg a támogatási mátrixot az operációs rendszerrel, CPU-architektúrával, virtualizációs háttérrel, guest architektúrával, szükséges eszközökkel, valamint a backup és restore támogatásával. A sikeres fordítás önmagában nem bizonyít platformtámogatást; az adott platformon a VM teljes életciklusát és a mentés-visszaállítás folyamatát is ki kell próbálni.

## Tervezési döntések rögzítése

Architecture Decision Recordot (ADR) használjunk, ha egy döntés érinti a platformhatárokat, a lemezen tárolt formátumokat, a kompatibilitást, a biztonságot vagy a migrációt. Az elfogadott döntések a `docs/decisions/` könyvtárba kerüljenek sorszámozott, angol fájlnévvel, például `0001-portable-backup-format.md`. Minden ADR tartalmazza az állapotot, a hátteret, a döntést, a mérlegelt alternatívákat és a következményeket.
