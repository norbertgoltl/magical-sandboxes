# OpenCode a magical-sandboxes rendszerben

Az `msbx run opencode` az OpenCode-ot az aktuális projekt tartós Debian VM-jében indítja. Az ugyanabból a kanonikus projektútvonalból indított munkamenetek közös VM-et és guest HOME-ot használnak. Más projektek külön VM-lemezt, OpenCode-profilt, belépést és beállításokat kapnak.

Az OpenCode natív guest útvonalai a `/home/msbx` alatt vannak, többek között a `/home/msbx/.local/share/opencode/auth.json`. Első alkalommal a guestben jelentkezz be. A credentialök és beállítások a projekt guest lemezén maradnak.

Inicializált célprojektből:

```sh
/útvonal/a/magical-sandboxes/bin/msbx init
/útvonal/a/magical-sandboxes/bin/msbx run opencode
```

Codex, Claude Code, OpenCode és shell munkamenetek futhatnak párhuzamosan egy projekt VM-jében. A guest agent a munkamenetek érkezésekor worker folyamatokat indít, nincs beállított darabszámkorlát; a gyakorlati párhuzamosságot a VM és a Mac erőforrásai szabják meg. A VM az utolsó munkamenet kilépése után három másodperccel leáll.

## Hibakeresés

- **Az OpenCode újra belépést kér:** ellenőrizd, hogy ugyanazt a projekt VM-lemezt használod-e. Az `auth.json` a guest lemezen van.
- **Másik projektben eltérő provider belépés van:** ez a várt működés; az OpenCode credentialök projektenként elkülönülnek.

A projekt VM törlése törli az OpenCode belépést és beállításokat.
