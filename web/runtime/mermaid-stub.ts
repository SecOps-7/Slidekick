// Stand-in for Mermaid in the lite export bundle (decks without diagrams).
export default {
  initialize() {},
  async render(): Promise<{ svg: string }> {
    throw new Error("diagrams are not included in this export");
  },
};
