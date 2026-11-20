import "../runtime/runtime.css";
import "./app.css";
import { mountHome } from "./home";
import { mountViewer } from "./viewer";

const app = document.getElementById("app")!;
const page = /^\/(login|setup|account|settings)\/?$/.exec(location.pathname)?.[1] as "login" | "setup" | "account" | "settings" | undefined;
const m = /^\/d\/([A-Za-z0-9_-]+)(?:\/(edit|present|print))?\/?$/.exec(location.pathname);
if (page) import("./account/pages").then((x) => x.mountAccountPage(app, page));
else if (m?.[2] === "edit") import("./editor/main").then((x) => x.mountEditor(app, m[1]));
else if (m?.[2] === "print") import("./print").then((x) => x.mountPrint(app, m[1]));
else if (m?.[2] === "present") import("./presenter").then((x) => x.mountPresenter(app, m[1]));
else if (m) mountViewer(app, m[1]);
else mountHome(app);
