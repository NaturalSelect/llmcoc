function app() {
    var core = COC.core();
    return Object.assign(core, COC.dashboard, COC.sessions, COC.game, COC.shop, COC.admin);
}
