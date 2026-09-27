# Codex a magical-sandboxes rendszerben

Az `msbx run codex` a natív Codex CLI-t az aktuális projekt tartós Debian VM-jében indítja el. Az ugyanabból a kanonikus projektkönyvtárból indított munkamenetek közösen használják a VM-et. Minden projekt külön VM-lemezt, Codex home-ot, bejelentkezést és beállításokat kap.

## Belépés és tartós állapot

A Codex `CODEX_HOME=/home/msbx/.codex` beállítással fut. Első indításkor a guest Codex felületén vagy CLI-jével jelentkezz be. Az `auth.json`, a projekt bizalmi állapota, a kiválasztott modell és más natív beállítások a projekt guest lemezén maradnak a Codex és a VM leállása után is. Másik projektben külön bejelentkezés szükséges.

A Codex wrapper a projekt VM-lemezén levő `/home/msbx/.codex` könyvtárat használja. Minden projekt VM-jében külön kell bejelentkezni és beállítani a Codexot. A guest Codex használatához nincs szükség host Codex telepítésére.

A Codex az app-server daemonját a `CODEX_HOME/packages/app-server-daemon/` alatt kezeli. A guest home végrehajtható, tartós guest fájlrendszeren van, ezért a daemon a kezelt telepítési útvonalról futtatható.

## Indítás és hibakeresés

Inicializált célprojektből:

```sh
/útvonal/a/magical-sandboxes/bin/msbx init
/útvonal/a/magical-sandboxes/bin/msbx run codex
```

Több Codex, shell és egyéb harness munkamenet is futhat párhuzamosan ugyanazon projekt VM-jében. A guest agent igény szerint növeli a worker poolt, nincs beállított darabszámkorlát; a gyakorlati párhuzamosságot a VM és a Mac erőforrásai határozzák meg. Az utolsó munkamenet kilépése után a VM három másodpercet vár, majd leáll; ezalatt egy új munkamenet ugyanahhoz a VM-hez csatlakozik.

- **Újra rákérdez a belépésre, modellre vagy mappabizalomra:** ellenőrizd, hogy ugyanazt a projektútvonalat és VM-lemezt használod-e. Ezek az adatok a guest lemezen levő `/home/msbx/.codex` alatt tárolódnak.
- **A kezelt app-server daemon nem futtatható:** ellenőrizd a guest lemez csatolását és a `/home/msbx/.codex/packages/app-server-daemon/` jogosultságait. Ne helyezd a `CODEX_HOME` könyvtárat a `/run` alá vagy a VirtioFS projektmegosztásba.
- **Az app server `File exists` hibát ad vagy nem éri el a control socketet:** a VM indulásakor törlődik a nem létező célpontra mutató socket symlink. Ha a hiba megmarad, vizsgáld meg a `/home/msbx/.codex/app-server-control/app-server-control.sock` útvonalat.
- **A host kapcsolat megszakadása után beragad:** nézd meg a repository `.msbx-dev/sandboxes/projects/<project-id>/` könyvtárában található `manager.log` fájlt és a guest `msbx-agent` naplóját. A guest agentnek jeleznie kell a folyamatcsoportnak, majd vissza kell adnia a munkafolyamatot a poolnak.
- **Másik projektben eltérő belépést látsz:** ez szándékos; a natív Codex hitelesítés projektenként elkülönül.

A projekt VM törlése a guest Codex belépési adatait és beállításait is törli.
