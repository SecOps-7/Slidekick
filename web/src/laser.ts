// A glowing laser-pointer dot drawn inside a slide stage, positioned in
// normalised (0..1) stage coordinates so it lands in the same spot at any size.
export class Laser {
  private dot: HTMLDivElement;
  private hideTimer = 0;

  constructor(private stage: HTMLElement) {
    this.dot = document.createElement("div");
    this.dot.className = "sk-laser";
    this.dot.setAttribute("aria-hidden", "true");
  }

  show(x: number, y: number) {
    // The player replaces the stage's children on every slide change; re-attach on top.
    if (this.stage.lastElementChild !== this.dot) this.stage.append(this.dot);
    this.dot.style.left = `${x * 100}%`;
    this.dot.style.top = `${y * 100}%`;
    this.dot.classList.add("on");
    window.clearTimeout(this.hideTimer);
    this.hideTimer = window.setTimeout(() => this.hide(), 4000);
  }

  hide() {
    this.dot.classList.remove("on");
  }
}
