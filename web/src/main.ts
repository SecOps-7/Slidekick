import "../runtime/runtime.css";
import "./app.css";
import { mountHome } from "./home";
import { mountViewer } from "./viewer";

const app = document.getElementById("app")!;
const m = /^\/d\/([A-Za-z0-9_-]+)\/?$/.exec(location.pathname);
if (m) mountViewer(app, m[1]);
else mountHome(app);
