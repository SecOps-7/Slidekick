import "../runtime/runtime.css";
import "./app.css";
import { mountHome } from "./home";
import { mountViewer } from "./viewer";

const app = document.getElementById("app")!;
const m = /^\/d\/([A-Za-z0-9_-]+)(?:\/(edit|present|print))?\/?$/.exec(location.pathname);
if (m?.[2] === "edit") import("./editor/main").then((x) => x.mountEditor(app, m[1]));
else if (m?.[2] === "print") import("./print").then((x) => x.mountPrint(app, m[1]));
else if (m?.[2] === "present") import("./presenter").then((x) => x.mountPresenter(app, m[1]));
else if (m) mountViewer(app, m[1]);
else mountHome(app);
