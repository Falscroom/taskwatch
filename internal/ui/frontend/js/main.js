// @ts-check
// Окно taskwatch: точка входа. Данные приходят с Go-части (internal/ui/httpapi),
// клики разбирает actions.js, вид держит свежим view.js.
import { listen } from "./actions.js";
import { $, toast } from "./dom.js";
import { loadConfig, saveSettings } from "./settings.js";
import { render, watchView } from "./view.js";

listen(document);
$("settings-form").addEventListener("submit", (e) => saveSettings(/** @type {SubmitEvent} */ (e)));

// Данные приходят через watchView; таймер только обновляет «N мин назад».
window.addEventListener("focus", render);
setInterval(render, 30000);

render();
loadConfig().catch((e) => toast(e.message)).finally(watchView);
