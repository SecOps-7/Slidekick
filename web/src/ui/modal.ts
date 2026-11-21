// Slidekick's own modal dialogs, replacing the browser's prompt(), confirm()
// and alert(). Each returns a promise, so callers read like the built-ins:
//
//   if (await confirmModal({ title: "Delete slide 3?", confirm: "Delete", danger: true })) ...
//   const name = await promptModal({ title: "New deck", label: "Title", value: "Untitled deck" });
//
// Built on <dialog>: focus is trapped while open, Esc and a click on the
// backdrop cancel, and focus returns to where it was.
import "./modal.css";

interface Base {
  title: string;
  message?: string;
}

export interface ConfirmOptions extends Base {
  confirm?: string; // button label, a verb ("Delete", "Revoke")
  cancel?: string;
  danger?: boolean; // red confirm button; Cancel gets the initial focus
}

export interface PromptOptions extends Base {
  label: string;
  value?: string;
  placeholder?: string;
  confirm?: string;
  cancel?: string;
  type?: "text" | "password";
  minLength?: number;
  autocomplete?: string;
  /** Returns an error message to show under the field, or null when the value is fine. */
  validate?: (value: string) => string | null;
}

export interface AlertOptions extends Base {
  ok?: string;
}

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string) => {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
};

let seq = 0;

/** Opens a dialog and resolves with whatever `done` is called with (Esc/backdrop -> onCancel()). */
function open<T>(build: (dlg: HTMLDialogElement, id: string, done: (v: T) => void) => HTMLElement | null, onCancel: () => T): Promise<T> {
  return new Promise((resolve) => {
    const before = document.activeElement as HTMLElement | null;
    const dlg = el("dialog", "skm");
    const id = `skm-${++seq}`;
    dlg.setAttribute("aria-labelledby", `${id}-t`);
    let settled = false;
    const done = (v: T) => {
      if (settled) return;
      settled = true;
      dlg.close();
      dlg.remove();
      before?.focus?.();
      resolve(v);
    };
    dlg.addEventListener("cancel", (e) => {
      e.preventDefault();
      done(onCancel());
    });
    dlg.addEventListener("mousedown", (e) => {
      if (e.target === dlg) done(onCancel()); // a click on the backdrop
    });
    const focusEl = build(dlg, id, done);
    document.body.append(dlg);
    dlg.showModal();
    focusEl?.focus();
  });
}

function header(o: Base, id: string): HTMLElement[] {
  const h = el("h2", "skm-title", o.title);
  h.id = `${id}-t`;
  const parts: HTMLElement[] = [h];
  if (o.message) for (const para of o.message.split("\n\n")) parts.push(el("p", "skm-msg", para));
  return parts;
}

function buttons(cancel: string | null, confirm: string, danger = false) {
  const foot = el("div", "skm-foot");
  let c: HTMLButtonElement | null = null;
  if (cancel !== null) {
    c = el("button", "skm-btn", cancel);
    c.type = "button";
    foot.append(c);
  }
  const ok = el("button", `skm-btn ${danger ? "danger" : "primary"}`, confirm);
  ok.type = "submit";
  foot.append(ok);
  return { foot, ok, cancel: c };
}

// For dialogs built elsewhere (the library's share dialog) from the same parts.
export { open as openModal, header as modalHeader, buttons as modalButtons };

/** Asks a yes/no question. Resolves true only when confirmed. */
export function confirmModal(o: ConfirmOptions): Promise<boolean> {
  return open<boolean>(
    (dlg, id, done) => {
      const form = el("form", "skm-body");
      const b = buttons(o.cancel ?? "Cancel", o.confirm ?? "OK", o.danger);
      form.append(...header(o, id), b.foot);
      form.addEventListener("submit", (e) => {
        e.preventDefault();
        done(true);
      });
      b.cancel!.onclick = () => done(false);
      dlg.append(form);
      if (o.danger) dlg.setAttribute("role", "alertdialog");
      return o.danger ? b.cancel : b.ok;
    },
    () => false,
  );
}

/** Asks for one value. Resolves with the trimmed text (passwords untrimmed), or null when cancelled. */
export function promptModal(o: PromptOptions): Promise<string | null> {
  return open<string | null>(
    (dlg, id, done) => {
      const form = el("form", "skm-body");
      form.noValidate = true;
      const field = el("label", "skm-field");
      const input = el("input", "skm-input");
      input.type = o.type ?? "text";
      input.value = o.value ?? "";
      if (o.placeholder) input.placeholder = o.placeholder;
      input.autocomplete = (o.autocomplete ?? (o.type === "password" ? "new-password" : "off")) as AutoFill;
      const err = el("p", "skm-err");
      err.id = `${id}-err`;
      err.setAttribute("aria-live", "polite");
      input.setAttribute("aria-describedby", err.id);
      field.append(el("span", "skm-label", o.label), input, err);
      const b = buttons(o.cancel ?? "Cancel", o.confirm ?? "OK");
      form.append(...header(o, id), field, b.foot);
      const check = () => {
        const v = input.value.trim();
        let msg = "";
        if (!v) msg = `Enter ${o.label.charAt(0).toLowerCase()}${o.label.slice(1)}.`;
        else if (o.minLength && input.value.length < o.minLength) msg = `Use at least ${o.minLength} characters.`;
        else msg = o.validate?.(v) ?? "";
        err.textContent = msg;
        input.setAttribute("aria-invalid", String(Boolean(msg)));
        return msg ? null : o.type === "password" ? input.value : v;
      };
      input.addEventListener("input", () => {
        if (err.textContent) check();
      });
      form.addEventListener("submit", (e) => {
        e.preventDefault();
        const v = check();
        if (v !== null) done(v);
        else input.focus();
      });
      b.cancel!.onclick = () => done(null);
      dlg.append(form);
      requestAnimationFrame(() => input.select());
      return input;
    },
    () => null,
  );
}

/** Shows text selected in a read-only field for copying by hand (when the clipboard is unavailable). */
export function copyModal(o: { title: string; text: string }): Promise<void> {
  return open<void>(
    (dlg, id, done) => {
      const form = el("form", "skm-body");
      const input = el("input", "skm-input");
      input.readOnly = true;
      input.value = o.text;
      input.setAttribute("aria-label", o.title);
      input.addEventListener("focus", () => input.select());
      const isMac = /Mac|iPhone|iPad/.test(navigator.platform);
      const b = buttons(null, "Done");
      form.append(...header({ title: o.title, message: `This browser blocked automatic copying. The text is selected: press ${isMac ? "⌘C" : "Ctrl+C"} to copy it.` }, id), input, b.foot);
      form.addEventListener("submit", (e) => {
        e.preventDefault();
        done();
      });
      dlg.append(form);
      requestAnimationFrame(() => input.select());
      return input;
    },
    () => undefined,
  );
}

/** Tells the person something. Resolves when they close it. */
export function alertModal(o: AlertOptions): Promise<void> {
  return open<void>(
    (dlg, id, done) => {
      const form = el("form", "skm-body");
      const b = buttons(null, o.ok ?? "OK");
      form.append(...header(o, id), b.foot);
      form.addEventListener("submit", (e) => {
        e.preventDefault();
        done();
      });
      dlg.append(form);
      return b.ok;
    },
    () => undefined,
  );
}
