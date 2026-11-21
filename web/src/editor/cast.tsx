// Editor toolbar wrapper for the shared cast button (see ../cast-menu.ts).
import { useEffect, useRef } from "preact/hooks";
import type { MutableRef } from "preact/hooks";
import { castMenu, type CastMenu } from "../cast-menu";

export function CastButton({ deckId, say, menuRef }: { deckId: string; say: (m: string) => void; menuRef: MutableRef<CastMenu | null> }) {
  const host = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    const m = castMenu(deckId, { buttonClass: "ed-btn", say });
    host.current?.append(m.el);
    menuRef.current = m;
    return () => {
      m.el.remove();
      menuRef.current = null;
    };
  }, [deckId]);
  return <span ref={host} />;
}
