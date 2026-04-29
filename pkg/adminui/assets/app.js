// goforge admin SPA. Vanilla ES modules - no framework, no build.
//
// The app talks to the goforge JSON API (`/api/v1/*`) exclusively.
// It enforces nothing itself: every permission gate lives on the
// server, so a user opening devtools and crafting their own fetch
// sees the same 403 they would see from the UI. The UI merely
// hides controls the server is guaranteed to reject, to reduce
// accidental clicks.
//
// Layout:
//  - api: thin fetch wrapper + token storage
//  - router: hash-based, one view per route
//  - views: per-route render functions, owning their own DOM
//
// Keep this file single-file for now; if it grows past ~1000 LOC
// we split per-view into their own modules and load them lazily.

const API = (() => {
    const TOKEN_KEY = "goforge.admin.token";
    const REFRESH_KEY = "goforge.admin.refresh";
    const ME_KEY = "goforge.admin.me";

    function getToken() { return localStorage.getItem(TOKEN_KEY); }
    function setTokens(access, refresh) {
        localStorage.setItem(TOKEN_KEY, access);
        if (refresh) localStorage.setItem(REFRESH_KEY, refresh);
    }
    function clear() {
        localStorage.removeItem(TOKEN_KEY);
        localStorage.removeItem(REFRESH_KEY);
        localStorage.removeItem(ME_KEY);
    }
    function getMe() {
        const raw = localStorage.getItem(ME_KEY);
        return raw ? JSON.parse(raw) : null;
    }
    function setMe(me) { localStorage.setItem(ME_KEY, JSON.stringify(me)); }

    async function request(method, path, body) {
        const headers = { "Accept": "application/json" };
        if (body !== undefined) headers["Content-Type"] = "application/json";
        const token = getToken();
        if (token) headers["Authorization"] = `Bearer ${token}`;

        const res = await fetch(`/api/v1${path}`, {
            method,
            headers,
            body: body === undefined ? undefined : JSON.stringify(body),
        });

        if (res.status === 401 && token) {
            // Token expired or revoked - bounce to login.
            clear();
            location.hash = "#/login";
            throw new Error("Session expired");
        }

        if (res.status === 204) return null;

        let payload = null;
        const ct = res.headers.get("content-type") ?? "";
        if (ct.includes("application/json")) payload = await res.json();

        if (!res.ok) {
            const code = payload?.error?.code ?? `http.${res.status}`;
            const msg = payload?.error?.message ?? res.statusText;
            const err = new Error(`${code}: ${msg}`);
            err.code = code;
            err.status = res.status;
            throw err;
        }
        // The API envelope wraps payloads as `{success: true, data: ...}`
        // - unwrap here so views don't repeat it on every call.
        if (payload && typeof payload === "object" && "data" in payload) {
            return payload.data;
        }
        return payload;
    }

    return {
        getToken,
        getMe,
        clear,
        async login(email, password) {
            const res = await fetch(`/api/v1/auth/login`, {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ email, password }),
            });
            const payload = await res.json();
            if (!res.ok) {
                const e = new Error(payload?.error?.message ?? "Login failed");
                e.code = payload?.error?.code;
                throw e;
            }
            const data = payload.data ?? payload;
            setTokens(data.tokens.access_token, data.tokens.refresh_token);
            setMe(data.user);
            return data;
        },
        async logout() {
            // Best-effort server-side revoke; clear local state regardless.
            try { await request("DELETE", "/me/sessions"); } catch { /* ignore */ }
            clear();
        },
        me: () => request("GET", "/auth/me"),
        myAccess: () => request("GET", "/me/access"),
        myMenu: () => request("GET", "/menus/mine"),

        users: {
            list: (q = "", offset = 0, limit = 50) =>
                request("GET", `/users?q=${encodeURIComponent(q)}&offset=${offset}&limit=${limit}`),
            assignRoles: (id, roleIDs) => request("PUT", `/users/${id}/roles`, { role_ids: roleIDs }),
        },
        permissions: {
            list: () => request("GET", "/permissions"),
            create: (body) => request("POST", "/permissions", body),
            update: (id, body) => request("PATCH", `/permissions/${id}`, body),
            remove: (id) => request("DELETE", `/permissions/${id}`),
        },
        roles: {
            list: () => request("GET", "/roles"),
            create: (body) => request("POST", "/roles", body),
            update: (id, body) => request("PATCH", `/roles/${id}`, body),
            remove: (id) => request("DELETE", `/roles/${id}`),
            listPerms: (id) => request("GET", `/roles/${id}/permissions`),
            setPerms: (id, permissionIDs) =>
                request("PUT", `/roles/${id}/permissions`, { permission_ids: permissionIDs }),
        },
        menus: {
            list: () => request("GET", "/menus/"),
            tree: () => request("GET", "/menus/tree"),
            create: (body) => request("POST", "/menus/", body),
            update: (id, body) => request("PATCH", `/menus/${id}`, body),
            remove: (id) => request("DELETE", `/menus/${id}`),
        },
        sessions: {
            list: () => request("GET", "/me/sessions"),
            revoke: (id) => request("DELETE", `/me/sessions/${id}`),
            revokeAll: () => request("DELETE", "/me/sessions"),
        },
        apiKeys: {
            list: () => request("GET", "/api-keys"),
            create: (body) => request("POST", "/api-keys", body),
            revoke: (id) => request("DELETE", `/api-keys/${id}`),
        },
    };
})();

// ---------- Small DOM helpers (no framework) ----------

function el(tag, attrs = {}, ...children) {
    const node = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) {
        if (k === "class") node.className = v;
        else if (k === "dataset") Object.assign(node.dataset, v);
        else if (k.startsWith("on") && typeof v === "function") node.addEventListener(k.slice(2), v);
        else if (v === true) node.setAttribute(k, "");
        else if (v === false || v == null) { /* skip */ }
        else node.setAttribute(k, v);
    }
    for (const c of children.flat()) {
        if (c == null || c === false) continue;
        node.appendChild(typeof c === "string" || typeof c === "number" ? document.createTextNode(String(c)) : c);
    }
    return node;
}

function clear(node) {
    while (node.firstChild) node.removeChild(node.firstChild);
}

function toast(msg, kind = "ok") {
    const t = document.getElementById("toast");
    t.textContent = msg;
    t.className = `toast ${kind}`;
    t.hidden = false;
    setTimeout(() => { t.hidden = true; }, 3500);
}

function err(msg) { toast(msg, "err"); }

function fmtDate(s) {
    if (!s) return "-";
    const d = new Date(s);
    if (isNaN(d.getTime())) return s;
    return d.toLocaleString();
}

function confirmDialog(title, message) {
    return new Promise((resolve) => {
        const backdrop = el("div", { class: "dialog-backdrop" });
        const dialog = el("div", { class: "dialog" },
            el("h3", {}, title),
            el("p", { class: "muted" }, message),
            el("div", { class: "row" },
                el("button", { class: "secondary", onclick: () => { backdrop.remove(); resolve(false); } }, "Cancel"),
                el("button", { class: "danger", onclick: () => { backdrop.remove(); resolve(true); } }, "Confirm"),
            ),
        );
        backdrop.appendChild(dialog);
        backdrop.addEventListener("click", (e) => { if (e.target === backdrop) { backdrop.remove(); resolve(false); } });
        document.body.appendChild(backdrop);
    });
}

function formDialog(title, fields, initial = {}) {
    return new Promise((resolve) => {
        const inputs = {};
        const form = el("form", { onsubmit: (e) => {
            e.preventDefault();
            const out = {};
            for (const f of fields) {
                const v = inputs[f.name].value;
                out[f.name] = f.type === "number" ? Number(v) : f.type === "checkbox" ? inputs[f.name].checked : v;
            }
            backdrop.remove();
            resolve(out);
        }});
        for (const f of fields) {
            const input = el("input", {
                type: f.type || "text",
                name: f.name,
                placeholder: f.placeholder || "",
                value: initial[f.name] ?? f.default ?? "",
                required: f.required,
            });
            if (f.type === "checkbox") {
                input.checked = !!(initial[f.name] ?? f.default);
            }
            inputs[f.name] = input;
            form.appendChild(el("label", {}, f.label || f.name, input));
        }
        form.appendChild(el("div", { class: "row" },
            el("button", { type: "button", class: "secondary", onclick: () => { backdrop.remove(); resolve(null); } }, "Cancel"),
            el("button", { type: "submit" }, "Save"),
        ));
        const backdrop = el("div", { class: "dialog-backdrop" },
            el("div", { class: "dialog" }, el("h3", {}, title), form),
        );
        backdrop.addEventListener("click", (e) => { if (e.target === backdrop) { backdrop.remove(); resolve(null); } });
        document.body.appendChild(backdrop);
        inputs[fields[0].name]?.focus();
    });
}

// ---------- Router ----------

const routes = [
    { path: "dashboard", label: "Dashboard", render: renderDashboard },
    { path: "users",     label: "Users",     render: renderUsers },
    { path: "roles",     label: "Roles",     render: renderRoles },
    { path: "permissions", label: "Permissions", render: renderPermissions },
    { path: "menus",     label: "Menus",     render: renderMenus },
    { path: "sessions",  label: "My sessions", render: renderSessions },
    { path: "api-keys",  label: "My API keys", render: renderAPIKeys },
    { path: "docs",      label: "OpenAPI",   render: renderDocs },
];

function currentRoute() {
    const hash = location.hash.replace(/^#\//, "").split("?")[0] || "dashboard";
    return routes.find((r) => r.path === hash) || routes[0];
}

function navigate(path) { location.hash = `#/${path}`; }

async function mount() {
    if (!API.getToken()) {
        showLogin();
        return;
    }
    document.getElementById("login-screen").hidden = true;
    document.getElementById("shell").hidden = false;

    const me = API.getMe() || await API.me();
    document.getElementById("me-email").textContent = me.email;

    renderNav();
    await renderCurrent();
    window.addEventListener("hashchange", renderCurrent);
}

function renderNav() {
    const nav = document.getElementById("nav");
    clear(nav);
    for (const r of routes) {
        nav.appendChild(el("a", { href: `#/${r.path}` }, r.label));
    }
    highlightNav();
}

function highlightNav() {
    const cur = currentRoute();
    for (const a of document.querySelectorAll("#nav a")) {
        a.classList.toggle("active", a.getAttribute("href") === `#/${cur.path}`);
    }
}

async function renderCurrent() {
    highlightNav();
    const view = document.getElementById("view");
    clear(view);
    try {
        await currentRoute().render(view);
    } catch (e) {
        err(e.message);
        view.appendChild(el("p", { class: "muted" }, e.message));
    }
}

function showLogin() {
    document.getElementById("login-screen").hidden = false;
    document.getElementById("shell").hidden = true;
    document.getElementById("login-form").onsubmit = async (e) => {
        e.preventDefault();
        const fd = new FormData(e.target);
        const btn = e.target.querySelector("button");
        const errBox = document.getElementById("login-error");
        errBox.hidden = true;
        btn.disabled = true;
        try {
            await API.login(fd.get("email"), fd.get("password"));
            location.hash = "#/dashboard";
            location.reload();
        } catch (ex) {
            errBox.textContent = ex.message;
            errBox.hidden = false;
        } finally {
            btn.disabled = false;
        }
    };
}

document.getElementById("logout").onclick = async () => {
    await API.logout();
    location.reload();
};

// ---------- Views ----------

async function renderDashboard(view) {
    view.appendChild(el("div", { class: "view-header" },
        el("div", {}, el("h2", {}, "Dashboard"), el("p", { class: "sub" }, "goforge admin panel overview.")),
    ));
    const grid = el("div", { class: "grid" });
    view.appendChild(grid);

    // Live health
    const healthCard = el("div", { class: "card" },
        el("div", { class: "label" }, "/healthz"),
        el("div", { class: "value", id: "h-live" }, "..."),
    );
    const readyCard = el("div", { class: "card" },
        el("div", { class: "label" }, "/readyz"),
        el("div", { class: "value", id: "h-ready" }, "..."),
    );
    const accessCard = el("div", { class: "card" },
        el("div", { class: "label" }, "My permissions"),
        el("div", { class: "value", id: "h-access" }, "..."),
    );
    grid.append(healthCard, readyCard, accessCard);

    // Health fetches are unauthenticated - use fetch directly.
    fetch("/healthz").then((r) => healthCard.querySelector("#h-live").textContent = r.ok ? "ok" : "fail")
        .then(() => healthCard.querySelector("#h-live").classList.add("ok"));
    fetch("/readyz").then((r) => r.json()).then((j) => {
        const target = readyCard.querySelector("#h-ready");
        target.textContent = j.status || (j.success ? "ready" : "down");
        target.classList.add(j.success === false ? "warn" : "ok");
    }).catch(() => readyCard.querySelector("#h-ready").textContent = "down");

    try {
        const access = await API.myAccess();
        const codes = (access.permissions ?? []).map((p) => p.code || p);
        const target = accessCard.querySelector("#h-access");
        target.textContent = codes.length;
    } catch (e) {
        accessCard.querySelector("#h-access").textContent = "-";
    }

    // Quick links to OpenAPI + metrics
    view.appendChild(el("h3", { style: "margin-top: 1.5rem" }, "Quick links"));
    view.appendChild(el("p", {},
        el("a", { href: "/docs", target: "_blank" }, "Swagger UI"), " · ",
        el("a", { href: "/openapi.json", target: "_blank" }, "openapi.json"), " · ",
        el("a", { href: "/metrics", target: "_blank" }, "/metrics"), " · ",
        el("a", { href: "/admin/modules", target: "_blank" }, "modules"),
    ));
}

async function renderUsers(view) {
    const state = {
        q: new URLSearchParams(location.hash.split("?")[1] || "").get("q") || "",
        offset: 0,
        limit: 50,
        data: null,
        roles: [],
    };

    view.appendChild(el("div", { class: "view-header" },
        el("div", {}, el("h2", {}, "Users"), el("p", { class: "sub" }, "Directory of registered users. Requires rbac.manage.")),
    ));

    const searchInput = el("input", { type: "search", placeholder: "Filter by email…", value: state.q });
    const prevBtn = el("button", { class: "secondary" }, "Prev");
    const nextBtn = el("button", { class: "secondary" }, "Next");
    const pageLabel = el("span", {}, "");
    const tbody = el("tbody");

    async function load() {
        try {
            const [users, roles] = await Promise.all([
                API.users.list(state.q, state.offset, state.limit),
                state.roles.length ? Promise.resolve({ items: state.roles }) : API.roles.list(),
            ]);
            state.data = users;
            state.roles = roles.items ?? roles;
            renderRows();
        } catch (e) { err(e.message); }
    }

    function renderRows() {
        clear(tbody);
        for (const u of state.data.items) {
            tbody.appendChild(el("tr", {},
                el("td", { class: "mono" }, u.id.slice(0, 8) + "…"),
                el("td", {}, u.email),
                el("td", {}, u.name || "-"),
                el("td", {}, u.role || "-"),
                el("td", {}, fmtDate(u.created_at)),
                el("td", { class: "actions" },
                    el("button", { class: "secondary", onclick: () => assignRoles(u) }, "Roles"),
                ),
            ));
        }
        pageLabel.textContent = `${state.offset + 1}–${Math.min(state.offset + state.data.items.length, state.data.total)} of ${state.data.total}`;
        prevBtn.disabled = state.offset === 0;
        nextBtn.disabled = state.offset + state.limit >= state.data.total;
    }

    async function assignRoles(u) {
        const current = new Set();
        try {
            // There's no GET /users/:id/roles endpoint; we could infer from /me/access for self,
            // but for arbitrary users the server does not expose a read-back. Fall back to empty
            // selection - the PUT call is idempotent (full replacement).
        } catch { /* noop */ }
        const form = el("form");
        const selected = new Set(current);
        for (const r of state.roles) {
            const cb = el("input", { type: "checkbox", value: r.id });
            if (selected.has(r.id)) cb.checked = true;
            cb.onchange = () => cb.checked ? selected.add(r.id) : selected.delete(r.id);
            form.appendChild(el("label", { style: "display:flex;align-items:center;gap:0.5rem;margin-bottom:0.25rem" },
                cb, `${r.code} — ${r.name || ""}`,
            ));
        }
        const backdrop = el("div", { class: "dialog-backdrop" },
            el("div", { class: "dialog" },
                el("h3", {}, `Assign roles to ${u.email}`),
                el("p", { class: "muted" }, "This replaces the user's current role set."),
                form,
                el("div", { class: "row" },
                    el("button", { class: "secondary", onclick: () => backdrop.remove() }, "Cancel"),
                    el("button", { onclick: async () => {
                        try {
                            await API.users.assignRoles(u.id, [...selected]);
                            toast("Roles assigned");
                        } catch (e) { err(e.message); }
                        backdrop.remove();
                    }}, "Save"),
                ),
            ),
        );
        document.body.appendChild(backdrop);
    }

    searchInput.oninput = () => { state.q = searchInput.value; state.offset = 0; load(); };
    prevBtn.onclick = () => { state.offset = Math.max(0, state.offset - state.limit); load(); };
    nextBtn.onclick = () => { state.offset += state.limit; load(); };

    view.appendChild(el("div", { class: "toolbar" }, searchInput));
    view.appendChild(el("table", {},
        el("thead", {}, el("tr", {},
            el("th", {}, "ID"), el("th", {}, "Email"), el("th", {}, "Name"),
            el("th", {}, "Role"), el("th", {}, "Created"), el("th", {}, ""))),
        tbody,
    ));
    view.appendChild(el("div", { class: "pager" }, prevBtn, pageLabel, nextBtn));

    await load();
}

async function renderPermissions(view) {
    view.appendChild(el("div", { class: "view-header" },
        el("div", {}, el("h2", {}, "Permissions"), el("p", { class: "sub" }, "Catalogue of permission codes. Run `forge rbac sync` to import from RequirePermission call-sites.")),
        el("button", { onclick: createPerm }, "New permission"),
    ));
    const tbody = el("tbody");
    view.appendChild(el("table", {},
        el("thead", {}, el("tr", {},
            el("th", {}, "Code"), el("th", {}, "Resource"), el("th", {}, "Action"),
            el("th", {}, "Description"), el("th", {}, ""))),
        tbody,
    ));

    async function load() {
        clear(tbody);
        const res = await API.permissions.list();
        for (const p of (res.items ?? res)) {
            tbody.appendChild(el("tr", {},
                el("td", { class: "mono" }, p.code),
                el("td", {}, p.resource || "-"),
                el("td", {}, p.action || "-"),
                el("td", {}, p.description || "-"),
                el("td", { class: "actions" },
                    el("button", { class: "secondary", onclick: () => editPerm(p) }, "Edit"),
                    el("button", { class: "danger", onclick: async () => {
                        if (!await confirmDialog("Delete permission", `Delete ${p.code}?`)) return;
                        try { await API.permissions.remove(p.id); toast("Deleted"); load(); } catch (e) { err(e.message); }
                    }}, "Delete"),
                ),
            ));
        }
    }

    async function createPerm() {
        const data = await formDialog("New permission", [
            { name: "code", label: "Code (resource.action)", required: true, placeholder: "users.read" },
            { name: "resource", label: "Resource", placeholder: "users" },
            { name: "action", label: "Action", placeholder: "read" },
            { name: "description", label: "Description" },
        ]);
        if (!data) return;
        try { await API.permissions.create(data); toast("Created"); load(); } catch (e) { err(e.message); }
    }

    async function editPerm(p) {
        const data = await formDialog("Edit permission", [
            { name: "code", label: "Code", required: true },
            { name: "resource", label: "Resource" },
            { name: "action", label: "Action" },
            { name: "description", label: "Description" },
        ], p);
        if (!data) return;
        try { await API.permissions.update(p.id, data); toast("Updated"); load(); } catch (e) { err(e.message); }
    }

    await load();
}

async function renderRoles(view) {
    view.appendChild(el("div", { class: "view-header" },
        el("div", {}, el("h2", {}, "Roles"), el("p", { class: "sub" }, "Roles bundle permissions. Assign roles to users in the Users tab.")),
        el("button", { onclick: createRole }, "New role"),
    ));
    const tbody = el("tbody");
    view.appendChild(el("table", {},
        el("thead", {}, el("tr", {},
            el("th", {}, "Code"), el("th", {}, "Name"),
            el("th", {}, "Description"), el("th", {}, "Permissions"),
            el("th", {}, ""))),
        tbody,
    ));

    async function load() {
        clear(tbody);
        const [roles, perms] = await Promise.all([API.roles.list(), API.permissions.list()]);
        const rolesList = roles.items ?? roles;
        const permList = perms.items ?? perms;

        for (const r of rolesList) {
            tbody.appendChild(el("tr", {},
                el("td", { class: "mono" }, r.code),
                el("td", {}, r.name || "-"),
                el("td", {}, r.description || "-"),
                el("td", {}, el("button", { class: "secondary", onclick: () => managePerms(r, permList) }, "Manage")),
                el("td", { class: "actions" },
                    el("button", { class: "secondary", onclick: () => editRole(r) }, "Edit"),
                    el("button", { class: "danger", onclick: async () => {
                        if (!await confirmDialog("Delete role", `Delete ${r.code}?`)) return;
                        try { await API.roles.remove(r.id); toast("Deleted"); load(); } catch (e) { err(e.message); }
                    }}, "Delete"),
                ),
            ));
        }
    }

    async function createRole() {
        const data = await formDialog("New role", [
            { name: "code", label: "Code", required: true, placeholder: "editors" },
            { name: "name", label: "Name", placeholder: "Editors" },
            { name: "description", label: "Description" },
        ]);
        if (!data) return;
        try { await API.roles.create(data); toast("Created"); load(); } catch (e) { err(e.message); }
    }

    async function editRole(r) {
        const data = await formDialog("Edit role", [
            { name: "code", label: "Code", required: true },
            { name: "name", label: "Name" },
            { name: "description", label: "Description" },
        ], r);
        if (!data) return;
        try { await API.roles.update(r.id, data); toast("Updated"); load(); } catch (e) { err(e.message); }
    }

    async function managePerms(r, permList) {
        const current = await API.roles.listPerms(r.id);
        const currentIDs = new Set((current.items ?? current).map((p) => p.id));

        const form = el("form");
        for (const p of permList) {
            const cb = el("input", { type: "checkbox", value: p.id });
            if (currentIDs.has(p.id)) cb.checked = true;
            cb.onchange = () => cb.checked ? currentIDs.add(p.id) : currentIDs.delete(p.id);
            form.appendChild(el("label", { style: "display:flex;align-items:center;gap:0.5rem;margin-bottom:0.25rem" },
                cb, el("span", { class: "chip perm" }, p.code),
            ));
        }

        const backdrop = el("div", { class: "dialog-backdrop" },
            el("div", { class: "dialog" },
                el("h3", {}, `Permissions for ${r.code}`),
                form,
                el("div", { class: "row" },
                    el("button", { class: "secondary", onclick: () => backdrop.remove() }, "Cancel"),
                    el("button", { onclick: async () => {
                        try { await API.roles.setPerms(r.id, [...currentIDs]); toast("Saved"); } catch (e) { err(e.message); }
                        backdrop.remove();
                    }}, "Save"),
                ),
            ),
        );
        document.body.appendChild(backdrop);
    }

    await load();
}

async function renderMenus(view) {
    view.appendChild(el("div", { class: "view-header" },
        el("div", {}, el("h2", {}, "Menus"), el("p", { class: "sub" }, "Tree of navigation items. Hidden to users missing required_permission_code.")),
        el("button", { onclick: () => createMenu(null) }, "New root"),
    ));

    const host = el("div", { class: "tree" });
    view.appendChild(host);

    async function load() {
        clear(host);
        const tree = await API.menus.tree();
        const items = tree.items ?? tree;
        host.appendChild(renderTree(items || []));
    }

    function renderTree(nodes) {
        const ul = el("ul");
        for (const n of nodes) {
            const node = el("div", { class: "node" },
                el("span", { class: "name" }, n.label || n.code),
                el("span", { class: "path" }, n.path || "-"),
                n.required_permission_code ? el("span", { class: "perm" }, `[${n.required_permission_code}]`) : null,
                el("button", { class: "secondary", onclick: () => createMenu(n.id) }, "+"),
                el("button", { class: "secondary", onclick: () => editMenu(n) }, "Edit"),
                el("button", { class: "danger", onclick: async () => {
                    if (!await confirmDialog("Delete menu", `Delete "${n.label || n.code}"?`)) return;
                    try { await API.menus.remove(n.id); toast("Deleted"); load(); } catch (e) { err(e.message); }
                }}, "Delete"),
            );
            const li = el("li", {}, node);
            if (n.children?.length) li.appendChild(renderTree(n.children));
            ul.appendChild(li);
        }
        return ul;
    }

    async function createMenu(parentID) {
        const data = await formDialog("New menu item", [
            { name: "code", label: "Code", required: true, placeholder: "settings" },
            { name: "label", label: "Label", required: true, placeholder: "Settings" },
            { name: "path", label: "Path / URL", placeholder: "/settings" },
            { name: "icon", label: "Icon" },
            { name: "sort_order", label: "Sort order", type: "number", default: 0 },
            { name: "required_permission_code", label: "Required permission (optional)", placeholder: "rbac.manage" },
            { name: "is_visible", label: "Visible", type: "checkbox", default: true },
        ]);
        if (!data) return;
        if (parentID) data.parent_id = parentID;
        try { await API.menus.create(data); toast("Created"); load(); } catch (e) { err(e.message); }
    }

    async function editMenu(m) {
        const data = await formDialog("Edit menu item", [
            { name: "label", label: "Label" },
            { name: "path", label: "Path" },
            { name: "icon", label: "Icon" },
            { name: "sort_order", label: "Sort order", type: "number" },
            { name: "required_permission_code", label: "Required permission" },
            { name: "is_visible", label: "Visible", type: "checkbox" },
        ], m);
        if (!data) return;
        try { await API.menus.update(m.id, data); toast("Updated"); load(); } catch (e) { err(e.message); }
    }

    await load();
}

async function renderSessions(view) {
    view.appendChild(el("div", { class: "view-header" },
        el("div", {}, el("h2", {}, "My sessions"), el("p", { class: "sub" }, "Devices signed into your account. Revoking a session invalidates its refresh-token chain.")),
        el("button", { class: "danger", onclick: async () => {
            if (!await confirmDialog("Logout other devices", "Every session except this one will be revoked.")) return;
            try { await API.sessions.revokeAll(); toast("Other sessions revoked"); renderSessions(view); } catch (e) { err(e.message); }
        }}, "Revoke others"),
    ));

    const res = await API.sessions.list();
    const items = res.items ?? res;
    const tbody = el("tbody");
    for (const s of items) {
        tbody.appendChild(el("tr", {},
            el("td", { class: "mono" }, s.id.slice(0, 8) + "…"),
            el("td", {}, s.current ? el("span", { class: "chip" }, "this device") : ""),
            el("td", {}, s.user_agent || "-"),
            el("td", {}, s.ip || "-"),
            el("td", {}, fmtDate(s.issued_at)),
            el("td", {}, fmtDate(s.last_used_at)),
            el("td", { class: "actions" },
                s.current ? null : el("button", { class: "danger", onclick: async () => {
                    if (!await confirmDialog("Revoke session", "This device will be signed out.")) return;
                    try { await API.sessions.revoke(s.id); toast("Revoked"); renderSessions(view); } catch (e) { err(e.message); }
                }}, "Revoke"),
            ),
        ));
    }
    view.appendChild(el("table", {},
        el("thead", {}, el("tr", {},
            el("th", {}, "ID"), el("th", {}, ""), el("th", {}, "User agent"),
            el("th", {}, "IP"), el("th", {}, "Issued"), el("th", {}, "Last used"), el("th", {}, ""))),
        tbody,
    ));
}

async function renderAPIKeys(view) {
    view.appendChild(el("div", { class: "view-header" },
        el("div", {}, el("h2", {}, "My API keys"), el("p", { class: "sub" }, "Scoped, rotatable tokens for service-to-service traffic. Plaintext is returned only once.")),
        el("button", { onclick: createKey }, "New API key"),
    ));

    const tbody = el("tbody");
    view.appendChild(el("table", {},
        el("thead", {}, el("tr", {},
            el("th", {}, "Prefix"), el("th", {}, "Name"), el("th", {}, "Scopes"),
            el("th", {}, "Last used"), el("th", {}, "Expires"), el("th", {}, ""))),
        tbody,
    ));

    async function load() {
        clear(tbody);
        const res = await API.apiKeys.list();
        for (const k of (res.items ?? res)) {
            tbody.appendChild(el("tr", {},
                el("td", { class: "mono" }, k.prefix || k.visible_prefix || k.id.slice(0, 8)),
                el("td", {}, k.name || "-"),
                el("td", {}, ...(k.scopes || []).map((s) => el("span", { class: "chip perm" }, s))),
                el("td", {}, fmtDate(k.last_used_at)),
                el("td", {}, fmtDate(k.expires_at)),
                el("td", { class: "actions" },
                    el("button", { class: "danger", onclick: async () => {
                        if (!await confirmDialog("Revoke API key", `Revoke ${k.name || k.prefix}?`)) return;
                        try { await API.apiKeys.revoke(k.id); toast("Revoked"); load(); } catch (e) { err(e.message); }
                    }}, "Revoke"),
                ),
            ));
        }
    }

    async function createKey() {
        const data = await formDialog("New API key", [
            { name: "name", label: "Name (for your reference)", required: true },
            { name: "scopes", label: "Scopes (space-separated)", placeholder: "reports.read orders.read" },
        ]);
        if (!data) return;
        const scopes = (data.scopes || "").trim().split(/\s+/).filter(Boolean);
        try {
            const res = await API.apiKeys.create({ name: data.name, scopes });
            const secret = res.plaintext || res.token || res.secret;
            const backdrop = el("div", { class: "dialog-backdrop" },
                el("div", { class: "dialog" },
                    el("h3", {}, "API key created"),
                    el("p", { class: "muted" }, "Copy this now. It will not be shown again."),
                    el("pre", { class: "json" }, secret),
                    el("div", { class: "row" },
                        el("button", { onclick: () => { navigator.clipboard.writeText(secret); toast("Copied"); } }, "Copy"),
                        el("button", { class: "secondary", onclick: () => { backdrop.remove(); load(); } }, "Close"),
                    ),
                ),
            );
            document.body.appendChild(backdrop);
        } catch (e) { err(e.message); }
    }

    await load();
}

async function renderDocs(view) {
    view.appendChild(el("div", { class: "view-header" },
        el("div", {}, el("h2", {}, "OpenAPI"), el("p", { class: "sub" }, "Raw /openapi.json — the same document used to generate the TypeScript SDK.")),
        el("a", { href: "/docs", target: "_blank", class: "chip" }, "Open Swagger UI"),
    ));
    try {
        const res = await fetch("/openapi.json");
        const spec = await res.json();
        const summary = {
            openapi: spec.openapi,
            info: spec.info,
            operations: Object.values(spec.paths || {}).reduce((acc, m) => acc + Object.keys(m).length, 0),
            tags: spec.tags?.map((t) => t.name) ?? [],
        };
        view.appendChild(el("pre", { class: "json" }, JSON.stringify(summary, null, 2)));
    } catch (e) {
        view.appendChild(el("p", { class: "muted" }, "Could not load /openapi.json: " + e.message));
    }
}

// ---------- Boot ----------

mount().catch((e) => {
    err(e.message);
    showLogin();
});
