# Claude Code a magical-sandboxes rendszerben

Az `msbx run claude` a natív Claude Code CLI-t az aktuális projekt tartós Debian VM-jében indítja. Az ugyanabból a kanonikus projektútvonalból indított munkamenetek közös VM-et használnak; a különböző projektek saját lemezt, guest home-ot, belépést és beállításokat kapnak.

A Claude `HOME=/home/msbx` és `CLAUDE_CONFIG_DIR=/home/msbx/.claude` beállítással fut. Első guest indításkor a Claude Code-ban jelentkezz be. A natív hitelesítés, mappabizalom, modell, effort és beállítások az adott projekt guest lemezén maradnak. Projektenként külön belépés kell.

Inicializált célprojektből:

```sh
/útvonal/a/magical-sandboxes/bin/msbx init
/útvonal/a/magical-sandboxes/bin/msbx run claude
```

Codex, Claude, OpenCode és shell munkamenetek futhatnak párhuzamosan ugyanazon projekt VM-jében. A guest agent új munkamenetek érkezésekor bővíti a worker poolt, nincs darabszámkorlát; a párhuzamosságot a VM és a Mac erőforrásai korlátozzák. Az utolsó munkamenet kilépése után három másodperccel a VM leáll.

## Hibakeresés

- **Újra megjelenik a mappabizalmi kérdés:** ellenőrizd a projektútvonalat és azt, hogy ugyanazt a `.msbx-dev/sandboxes/projects/<project-id>/disk.raw` lemezt használod-e. A Claude bizalmi állapota a guest lemezen levő `/home/msbx/.claude` alatt van.
- **Natív CLI-figyelmeztetés vagy hiányzó launcher:** a wrapper azt várja, hogy a `/home/msbx/.local/bin/claude` a `/home/msbx/.local/share/claude/versions/` alá mutasson. Hiányzó natív telepítőfájlok esetén építsd újra és provisionáld a sablont; a meglévő projektes VM-ek nem frissülnek automatikusan.
- **Másik projektben másik belépés van:** ez a várt működés; minden projekt VM-je külön Claude profilt tartalmaz.

A projekt VM törlése a Claude belépést és beállításokat is törli.
