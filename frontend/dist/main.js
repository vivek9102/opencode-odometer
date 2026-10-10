// OpenCode Odometer frontend.
// Talks to the Go backend through Wails bindings (window.go.wservice.Service).

const Svc = () => window.go.wservice.Service;
const $ = (id) => document.getElementById(id);

const MODE_TIP = {
  warn: "warn  - never blocks, toast only",
  soft: "soft  - blocks at 100%, one grace turn,\n        hard stop at {hard}x limit",
  hard: "hard  - blocks at 100% immediately",
};

let snapshot = null; // last snapshot, for menu actions and the advice screen

// ---- odometer digit rendering (mirrors the Python beveled cells) ----
function odometerHTML(kind) {
  // kind: 'bar' (4 whole + 4 frac) or 'big' (5 whole + 5 frac)
  const wholeN = kind === "big" ? 5 : 4;
  const fracN = kind === "big" ? 5 : 4;
  return {
    wholeN, fracN,
    build(costStr, accent) {
      const [w, f] = costStr.split(".");
      const whole = w.padStart(wholeN, "0");
      const frac = (f || "").padEnd(fracN, "0").slice(0, fracN);
      let h = `<span class="stripe" style="background:${accent}"></span>`;
      h += `<span class="dsym" style="color:${accent}">$</span>`;
      for (const d of whole) h += `<span class="digit whole">${d}</span>`;
      h += `<span class="dp ${kind === 'big' ? 'big' : ''}" style="background:${accent}"></span>`;
      for (const d of frac) h += `<span class="digit frac">${d}</span>`;
      return h;
    },
  };
}

const BAND = {
  ok:     "#22c55e", // green
  warn:   "#f59e0b", // amber  - approaching the cap
  over:   "#fb923c", // orange - past the cap, recoverable
  hard:   "#ef4444", // red    - past the hard stop, nothing will save you
};

// budgetBand maps a snapshot onto one of four colours.
//
// "over" and "past hard stop" were previously both red, which hid the single
// most useful distinction: whether raising the limit or spending a grace turn
// can still rescue the session. Fraction arrives unclamped precisely so this
// can be told apart.
//
// In warn mode nothing is ever refused, so amber is the ceiling - painting it
// red would claim a block that will not happen.
function budgetBand(snap) {
  if (!snap.budget_enabled || !(snap.limit > 0)) return null;
  const warnAt = snap.warn_at > 0 && snap.warn_at < 1 ? snap.warn_at : 0.8;
  const frac = snap.fraction || 0;

  if (snap.mode === "warn") return frac >= warnAt ? BAND.warn : BAND.ok;

  // hard mode has no recoverable band: 100% is already terminal.
  if (snap.mode === "hard") {
    if (frac >= 1) return BAND.hard;
    return frac >= warnAt ? BAND.warn : BAND.ok;
  }

  // soft mode
  const hardAt = snap.hard_stop_at > 1 ? snap.hard_stop_at : 1.5;
  if (snap.past_hard_stop || frac >= hardAt) return BAND.hard;
  if (frac >= 1) return BAND.over;
  return frac >= warnAt ? BAND.warn : BAND.ok;
}

function accentFor(snap) {
  // Budget state wins over trip/total colouring - it is the urgent signal.
  const band = budgetBand(snap);
  if (band && band !== BAND.ok) return band;
  // Nothing priced means the reading is not a measurement. Showing it in the
  // usual confident green would claim "you spent nothing" when the truth is
  // "this could not be priced".
  if (allSpendUnpriced(snap)) return "#a1a1aa";
  return snap.view === "TOTAL" ? "#ef4444" : "#22c55e";
}

// allSpendUnpriced reports that tokens were used but nothing could be priced,
// which is the case where a $0.00 reading is actively misleading.
function allSpendUnpriced(snap) {
  return (snap.unpriced_models || 0) > 0 &&
         (snap.cost || 0) === 0 &&
         (snap.unpriced_tokens || 0) > 0;
}

// Clamp so a runaway total cannot overflow the digit cells.
function clampCost(v, kind) {
  const max = kind === "big" ? 99999.99999 : 9999.9999;
  if (!isFinite(v) || v < 0) return 0;
  return Math.min(v, max);
}

function renderOdometer(el, kind, snap) {
  const accent = accentFor(snap);
  const reading = snap.preferences?.remaining && snap.budget_enabled && snap.limit > 0 ? Math.max(0,snap.limit - snap.session_cost) : snap.cost;
  const costStr = clampCost(reading, kind).toFixed(kind === "big" ? 5 : 4);
  let h = odometerHTML(kind).build(costStr, accent);
  if (snap.active) h += `<span class="pulse"></span>`;
  el.innerHTML = h;
}

// ---- generic renderer ----
function human(n) {
  if (n >= 1e9) return (n / 1e9).toFixed(1) + "B";
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(1) + "K";
  return String(n);
}

// ---- table sorting (the headers were marked sortable but did nothing) ----
let sortCol = "cost";
let sortDesc = true;

function sortRows(rows) {
  const out = rows.slice();
  out.sort((a, b) => {
    let x = a[sortCol], y = b[sortCol];
    let cmp;
    if (typeof x === "string" || typeof y === "string") {
      cmp = String(x).toLowerCase().localeCompare(String(y).toLowerCase());
    } else {
      cmp = x - y;
    }
    if (sortDesc) cmp = -cmp;
    // Break ties on the model key. Free models all cost exactly 0, so
    // without a stable tiebreak they swapped places on every refresh and the
    // bottom of the table appeared to shuffle by itself.
    if (cmp === 0) return String(a.key).localeCompare(String(b.key));
    return cmp;
  });
  return out;
}

function paintSortArrows() {
  for (const th of document.querySelectorAll("th.sortable")) {
    const arrow = th.querySelector(".sort-arrow");
    if (!arrow) continue;
    arrow.textContent = th.dataset.col === sortCol ? (sortDesc ? "\u25BC" : "\u25B2") : "";
  }
}

for (const th of document.querySelectorAll("th.sortable")) {
  th.addEventListener("click", () => {
    const col = th.dataset.col;
    if (col === sortCol) sortDesc = !sortDesc;
    else { sortCol = col; sortDesc = true; }
    paintSortArrows();
    if (snapshot) render(snapshot);
  });
}

// shortSid trims an OpenCode session id to something readable.
function shortSid(sid) {
  if (!sid) return "";
  return sid.startsWith("ses_") ? sid.slice(4, 12) : sid.slice(0, 8);
}

// budgetLabelText shows one number: this chat against its limit.
//
// Earlier revisions also printed the other sessions' combined spend, which
// read as a second budget and made the line harder to parse than the thing it
// was explaining. The detail lives in the tooltip instead.
function budgetLabelText(snap) {
  if (!snap.budget_enabled) return "off";
  const pct = Math.round((snap.fraction || 0) * 100);
  if (snap.preferences?.paused) return `paused · $${(snap.session_cost || 0).toFixed(2)} counted`;
  let s = `$${(snap.session_cost || 0).toFixed(2)} / $${(snap.limit || 0).toFixed(2)}  ${pct}%`;
  // A limit that is set but cannot block otherwise reads as armed.
  if (!snap.enforced) s += "  (warn only)";
  return s;
}

function budgetLabelTip(snap) {
  if (!snap.budget_enabled) return "Session limit is off.";
  const lines = ["The limit applies to this chat only. Each chat gets its own."];
  const others = (snap.session_count || 1) - 1;
  if (others > 0 && !Array.isArray(snap.open_sessions)) {
    lines.push(`${others} other chat${others > 1 ? "s" : ""}: $${(snap.other_cost || 0).toFixed(2)} (not counted here)`);
  }
  if (!snap.enforced) lines.push("", "Mode is 'warn' - nothing will be blocked.");
  else if (snap.mode === "soft") lines.push("", `Blocks at 100%, one grace turn, stops at ${snap.hard_stop_at}x.`);
  else if (snap.mode === "hard") lines.push("", "Blocks immediately at 100%.");
  return lines.join("\n");
}

function render(snap) {
	 snap = selectedSnapshot(snap);
  snapshot = snap;
  document.body.classList.toggle("compact-mode", snap.compact);
  const globalSnap = Array.isArray(snap.open_sessions) ? {...snap,budget_enabled:false,preferences:{...snap.preferences,remaining:false}} : snap;
  renderExperience(globalSnap);
  $("bd-reading").disabled = Array.isArray(snap.open_sessions);

  // compact bar
  renderOdometer($("bar-odometer"), "bar", snap);
  const rate = snap.rate || 0;
  $("bar-rate").textContent = snap.preferences?.paused ? "PAUSED" : snap.preferences?.remaining && snap.budget_enabled ? "REMAINING" : "$" + rate.toFixed(2) + "/hr";
  $("bar-rate").style.color = rate > 5 ? "var(--red)" : rate > 0 ? "#e4e4e7" : "var(--dim)";
  $("bar-model").textContent = snap.connected ? snap.model : "offline";
  $("bar-model").style.color = snap.connected ? "var(--dim)" : "var(--amber)";
	 renderSessionDock(snap);

  // board
  $("bd-mode").textContent = snap.view;
  $("bd-mode").style.color = snap.view !== "TOTAL" ? "var(--green)" : "var(--red)";
  $("bd-reset").classList.toggle("hidden",snap.view!=="TRIP");
  document.querySelectorAll("[data-counter]").forEach(e=>e.classList.toggle("on",e.dataset.counter===snap.view));
  // State the gap rather than reporting a total that omits it.
  $("bd-status").textContent = allSpendUnpriced(snap)
    ? "no prices - spend not counted"
    : "saved $" + snap.saved.toFixed(4);
  $("bd-status").style.color = allSpendUnpriced(snap) ? "var(--amber)" : "";
  renderOdometer($("bd-odometer"), "big", globalSnap);
  $("bd-rate").textContent = "$" + rate.toFixed(2) + "/hr";
  $("bd-saved").textContent = "$" + snap.saved.toFixed(4);
  $("bd-tokens").textContent = human(snap.tokens);
  $("bd-model").textContent = snap.model;

  // connection indicator: an unreachable server used to look like "no spend"
  const conn = $("bd-conn");
  if (snap.connected) {
    conn.textContent = "\u25CF live";
    conn.className = "conn ok";
    conn.title = "Connected to " + (snap.server_url || "OpenCode");
  } else {
    conn.textContent = "\u25CF offline";
    conn.className = "conn off";
    conn.title = "Cannot reach OpenCode" + (snap.server_url ? " at " + snap.server_url : "") +
      ". Start OpenCode; the odometer reconnects automatically.";
  }

  // budget controls. Do not fight the user while they are typing.
  const limitEl = $("bd-limit");
  const modeSel = $("bd-mode-sel");
  if(Array.isArray(snap.open_sessions))renderOpenBudgetControls(snap);
  else {
    $("bd-enabled").checked = !!snap.budget_enabled;
    if (document.activeElement !== limitEl) limitEl.value = String(snap.limit);
    if (document.activeElement !== modeSel) modeSel.value = snap.mode;
  }
  const band = budgetBand(snap);
  $("bd-budget-label").textContent = budgetLabelText(snap);
  $("bd-budget-label").title = budgetLabelTip(snap);
  $("bd-budget-label").style.color = !snap.budget_enabled ? "var(--dim)" : (band || "var(--green)");

  const fill = $("bd-bar-fill");
  // Width clamps; colour does not. Past the cap the bar is full but keeps
  // changing colour, so "over" and "hard stop" stay distinguishable.
  fill.style.width = Math.round(Math.min(snap.fraction || 0, 1) * 100) + "%";
  fill.style.background = band || "var(--green)";
  const warnAt = snap.warn_at > 0 && snap.warn_at < 1 ? snap.warn_at : 0.8;
  $("bd-bar-warn").style.left = Math.round(warnAt * 100) + "%";

  if (snap.hard_stop_at > 0) modeSel.dataset.hard = snap.hard_stop_at;

  // Unpriced warning on the compact bar: the board's button is invisible in
  // the docked view most users keep open.
  const nUnpriced = snap.unpriced_models || 0;
  const barWarn = $("bar-unpriced");
  barWarn.classList.toggle("hidden", nUnpriced === 0);
  if (nUnpriced > 0) {
    barWarn.title = nUnpriced === 1
      ? "1 model has no price - its spend is NOT counted. Expand to set it."
      : nUnpriced + " models have no price - their spend is NOT counted. Expand to set them.";
  }

  const sid = shortSid(snap.session_id);
  $("bd-stop").title = sid
    ? `Abort the active session (${sid}) in OpenCode. Other sessions keep running.`
    : "Abort the active session in OpenCode.";
	 renderOpenSessions(snap);

  // table
  const tb = $("bd-tbody");
  tb.innerHTML = "";
  if (!snap.rows || snap.rows.length === 0) {
    const tr = document.createElement("tr");
    const td = document.createElement("td");
    td.colSpan = 6;
    td.className = "empty-note";
    td.textContent = "No spend recorded yet.";
    tr.appendChild(td);
    tb.appendChild(tr);
  }
  for (const r of sortRows(snap.rows || [])) {
    const tr = document.createElement("tr");
    if (r.free) tr.className = "free";
    else if (r.unknown) tr.className = "unknown";
    if (r.estimated) tr.classList.add("estimated");
    const name = r.free ? "\u2022 " + r.key : r.unknown ? "? " + r.key : "  " + r.key;
    // "~" marks a rate borrowed from the same model under another provider.
    const cost = r.free ? "FREE"
      : r.unknown ? "\u2014"
      : (r.estimated ? "~" : "") + r.cost.toFixed(4);
    const cells = [name, r.msgs, human(r.in), human(r.out), human(r.cache), cost];
    cells.forEach((c, i) => {
      const td = document.createElement("td");
      if (i > 0) td.className = "num";
      td.textContent = c;
      tr.appendChild(td);
    });
    tr.title = r.source
      ? `${r.key} — cost source: ${r.source}`
      : r.key;
    tb.appendChild(tr);
  }

  // Keep pricing details inside the review screen; each estimated table
  // amount still carries its ~ marker.
  const est = $("bd-estimated");
  if (snap.estimated_models > 0) {
    est.textContent = `${snap.estimated_models} model${snap.estimated_models===1 ? " uses" : "s use"} estimated rates. Estimated amounts are marked ~ in the spending table.`;
    est.title = "Priced from the same model under another provider. " +
      "Hover a ~ row for its source, or set exact rates in prices.local.json.";
    est.classList.remove("hidden");
  } else {
    est.classList.add("hidden");
  }

  checkAdvice(snap);
  checkToast(snap);
  if(typeof renderBudgetRules==="function")renderBudgetRules(snap);
}

// ---- view switching ----
function setView(toBoard) {
  cancelPeek();
  $("bar").classList.toggle("hidden", toBoard);
  $("board").classList.toggle("hidden", !toBoard);
  Svc().SetCompact(!toBoard); // also docks (compact) or centers (board)
}

// ---- clipboard ----
function copyText(text, msg) {
  const done = () => toast(msg || ("Copied " + text), "ok");
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(done).catch(() => fallbackCopy(text, done));
  } else {
    fallbackCopy(text, done);
  }
}
function fallbackCopy(text, done) {
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  try { document.execCommand("copy"); done(); } catch { /* ignore */ }
  document.body.removeChild(ta);
}

// ---- tooltip ----
const tip = $("tip");
function showTip(e, text) {
  tip.textContent = text;
  tip.classList.remove("hidden");
  const r = e.target.getBoundingClientRect();
  tip.style.left = r.left + "px";
  tip.style.top = (r.bottom + 4) + "px";
}
function hideTip() { tip.classList.add("hidden"); }

function modeTipText() {
  const sel = $("bd-mode-sel");
  return (MODE_TIP[sel.value] || MODE_TIP.soft).replace("{hard}", String(sel.dataset.hard || 1.5));
}

// ---- controls ----
$("bar-expand").addEventListener("click", expandSessions);
$("bd-collapse").addEventListener("click", () => setView(false));

$("bd-enabled").addEventListener("change", (e) => Array.isArray(snapshot?.open_sessions)?saveOpenBudget(e.target.checked):Svc().SetEnabled(e.target.checked));
$("bd-limit").addEventListener("change", (e) => {
  const v = parseFloat(e.target.value);
  if(Array.isArray(snapshot?.open_sessions)){
    // An amount typed before ENABLE is a draft. Auto-enabling on blur would
    // tick the checkbox before its click toggles it, immediately clearing it.
    if($("bd-enabled").checked)saveOpenBudget(true);
    return;
  }
  if (!isNaN(v) && v >= 0) Svc().SetLimit(v);
  else if (snapshot) e.target.value = String(snapshot.limit);
});
$("bd-limit").addEventListener("keydown", (e) => { if (e.key === "Enter") e.target.blur(); });
$("bd-mode-sel").addEventListener("change", (e) => Array.isArray(snapshot?.open_sessions)?saveOpenBudget($("bd-enabled").checked):Svc().SetMode(e.target.value));
$("bd-mode-sel").addEventListener("mouseenter", (e) => showTip(e, modeTipText()));
$("bd-mode-sel").addEventListener("mouseleave", hideTip);

$("bd-toggle").addEventListener("click", () => Svc().ToggleViewMode());
$("bd-reset").addEventListener("click", () => {
  if (confirm("Reset the trip counter? Lifetime totals are kept.")) {
    Svc().ResetTrip().then(() => toast("Trip reset", "ok"));
  }
});
// Download the latest catalogue for future messages. Recorded history remains
// fixed, just like it does when a user saves an unknown-provider price.
$("bd-update-prices").addEventListener("click", () => {
  toast("Fetching latest price catalog\u2026", "ok");
  Svc().RefreshPrices()
    .then((msg) => {
      toast(msg, String(msg).startsWith("error:") ? "over" : "ok");
      refreshUnpriced();
    })
    .catch((err) => toast("Update failed: " + err, "over"));
});
$("bd-dock").addEventListener("click", () => {
  setView(false);
});

let stoppingSession=false;
async function stopSession(fromAdvice=false,entryID=snapshot?.selected_session) {
  if(stoppingSession)return;
  stoppingSession=true;
  $("bd-stop").disabled=$("advice-stop").disabled=true;
  toast("Stopping selected session…","warn");
  try {if(Array.isArray(snapshot?.open_sessions))await Svc().StopOpenSession(entryID);else await Svc().AbortCurrent();if(fromAdvice)closeAdvice();toast("OpenCode stopped the chat","ok");}
  catch(err){toast("Stop failed: "+err,"over");}
  finally{stoppingSession=false;$("bd-stop").disabled=$("advice-stop").disabled=false;if(snapshot){renderOpenSessions(snapshot);renderSessionDock(snapshot);}}
}
$("bd-stop").addEventListener("click",()=>stopSession());
$("bd-export").addEventListener("click", () => {
  const p = Svc().ExportCsv();
  if (p && p.then) p.then((r) => toast(String(r).startsWith("error:") ? r : "Exported: " + r,
    String(r).startsWith("error:") ? "over" : "ok"));
});

// ---- prices needing confirmation ----
//
// An unpriced model is not just a cosmetic gap: it contributes $0 to the
// session total, so its spend never reaches the budget and cannot trigger a
// limit. The backend already computed suggestions; this surfaces them.
const priceModal = $("price-modal");

function refreshUnpriced() {
  if (!window.go || !window.go.wservice) return;
  Svc().PendingPriceSuggestions().then((list) => {
    const prices = list || [];
    const n = prices.filter((s) => s.needs_confirmation).length;
    const btn = $("bd-unpriced");
    btn.classList.remove("hidden");
    btn.classList.toggle("unpriced", n > 0);
    btn.textContent = n > 0
      ? "REVIEW " + n + " PRICE" + (n===1 ? "" : "S")
      : "REVIEW PRICES";
  }).catch(() => { /* backend not ready */ });
}

function openPriceModal() {
  Svc().PendingPriceSuggestions().then((list) => {
    const wrap = $("price-list");
    wrap.innerHTML = "";
    if (!list || !list.length) {
      const d = document.createElement("div");
      d.className = "model-desc";
      d.textContent = "No unknown-provider prices have been used yet.";
      wrap.appendChild(d);
    }
    for (const sug of list || []) {
      const r = sug.suggested_rate || {};
      const card = document.createElement("div");
      card.className = "model-card";

      const info = document.createElement("div");
      info.className = "model-info";

      const title = document.createElement("div");
      title.className = "model-title";
      title.textContent = bareModel(sug.key);
      const prov = providerOf(sug.key);
      if (prov) {
        const p = document.createElement("span");
        p.className = "prov-tag";
        p.textContent = prov;
        title.appendChild(p);
      }

      const desc = document.createElement("div");
      desc.className = "model-desc";
      desc.textContent = sug.source || "unknown model";

      // Input/output are the common adjustment. Cache keeps the matched
      // original provider's rates unless the user explicitly opens it.
      const row = document.createElement("div");
      row.className = "rate-row";
      const fields = {};
      for (const f of [
        { k: "input", label: "in" },
        { k: "output", label: "out" },
      ]) {
        const l = document.createElement("label");
        l.textContent = f.label;
        const inp = document.createElement("input");
        inp.type = "text";
        inp.className = "rate-in";
        inp.value = String(r[f.k] !== undefined ? r[f.k] : 0);
        fields[f.k] = inp;
        row.appendChild(l);
        row.appendChild(inp);
      }

      const cacheToggle = document.createElement("label");
      cacheToggle.className = "cache-toggle";
      const cacheCheck = document.createElement("input");
      cacheCheck.type = "checkbox";
      cacheToggle.appendChild(cacheCheck);
      cacheToggle.appendChild(document.createTextNode(" customize cache prices"));

      const cacheRow = document.createElement("div");
      cacheRow.className = "rate-row cache-rate-row hidden";
      for (const f of [
        { k: "cache_read", label: "cache read" },
        { k: "cache_write", label: "cache write" },
      ]) {
        const l = document.createElement("label");
        l.textContent = f.label;
        const inp = document.createElement("input");
        inp.type = "text";
        inp.className = "rate-in";
        inp.value = String(r[f.k] !== undefined ? r[f.k] : 0);
        fields[f.k] = inp;
        cacheRow.appendChild(l);
        cacheRow.appendChild(inp);
      }
      cacheCheck.addEventListener("change", () => {
        cacheRow.classList.toggle("hidden", !cacheCheck.checked);
      });

      const cacheNote = document.createElement("div");
      cacheNote.className = "cache-note";
      if (sug.is_local) {
        cacheNote.textContent = "Cache keeps the saved values unless customized.";
      } else if (String(sug.source || "").startsWith("estimate from ")) {
        cacheNote.textContent = "Cache uses " + sug.source.slice("estimate from ".length) + " unless customized.";
      } else {
        cacheNote.textContent = "Cache uses the suggested family defaults unless customized.";
      }

      info.appendChild(title);
      info.appendChild(desc);
      info.appendChild(row);
      info.appendChild(cacheToggle);
      info.appendChild(cacheRow);
      info.appendChild(cacheNote);

      const save = document.createElement("button");
      save.className = "model-pick-btn";
      save.textContent = sug.is_local ? "UPDATE PRICE" : "USE / SAVE";
      save.title = "Use this local price for new messages. Recorded history stays unchanged.";
      save.addEventListener("click", () => {
        const rate = { name: bareModel(sug.key), family: r.family || "" };
        let bad = false;
        for (const k of Object.keys(fields)) {
          const v = parseFloat(fields[k].value);
          if (isNaN(v) || v < 0) { bad = true; break; }
          rate[k] = v;
        }
        if (bad) { toast("Rates must be numbers >= 0", "over"); return; }
        Svc().ApplyPriceOverride(sug.key, rate)
          .then(() => {
            toast("Saved price for " + bareModel(sug.key), "ok");
            refreshUnpriced();
            openPriceModal();
          })
          .catch((err) => toast("Save failed: " + err, "over"));
      });

      const free = document.createElement("button");
      free.className = "model-pick-btn";
      free.style.background = "#166534";
      free.textContent = "MARK FREE";
      free.title = "Treat new messages from this exact provider/model as free. History stays unchanged.";
      free.addEventListener("click", () => {
        const rate = {
          name: bareModel(sug.key), family: r.family || "", free: true,
          input: 0, output: 0, cache_read: 0, cache_write: 0,
        };
        Svc().ApplyPriceOverride(sug.key, rate)
          .then(() => {
            toast("Marked " + bareModel(sug.key) + " free", "ok");
            refreshUnpriced();
            openPriceModal();
          })
          .catch((err) => toast("Save failed: " + err, "over"));
      });

      const skip = document.createElement("button");
      skip.className = "model-pick-btn";
      skip.style.background = "#3f3f46";
      skip.textContent = "IGNORE";
      skip.title = sug.is_unknown
        ? "Stop asking. This unknown model remains uncounted."
        : "Stop asking. The displayed cross-provider estimate remains in use.";
      skip.addEventListener("click", () => {
        Svc().SkipPrice(sug.key);
        toast("Ignoring " + bareModel(sug.key), "ok");
        refreshUnpriced();
        openPriceModal();
      });

      const btns = document.createElement("div");
      btns.className = "price-actions";
      btns.appendChild(save);
      btns.appendChild(free);
      if (!sug.is_local) btns.appendChild(skip);

      card.appendChild(info);
      card.appendChild(btns);
      wrap.appendChild(card);
    }

    if ((list || []).some((s) => s.is_local)) {
      const reset = document.createElement("button");
      reset.className = "ctl reset-provider-prices";
      reset.textContent = "RESET SAVED PRICES";
      reset.title = "Remove every custom provider price. New messages return to public-provider estimates; history stays unchanged.";
      reset.addEventListener("click", () => {
        if (!confirm("Reset all saved custom provider prices?\n\nNew messages will use public-provider estimates. Recorded history will not change.")) return;
        Svc().ClearPriceOverrides()
          .then(() => {
            toast("Saved provider prices reset", "ok");
            refreshUnpriced();
            openPriceModal();
          })
          .catch((err) => toast("Reset failed: " + err, "over"));
      });
      wrap.appendChild(reset);
    }
    priceModal.classList.remove("hidden");
  });
}

function closePriceModal() { priceModal.classList.add("hidden"); }

// Keep catalog age in the tooltip so it cannot push session controls onto
// their own row as the catalog gets older.
function refreshPricesAge() {
  if (!window.go || !window.go.wservice) return;
  Svc().PricesAgeHours().then((h) => {
    const btn = $("bd-update-prices");
    btn.textContent = "UPDATE CATALOG";
    // -1 means the built-in snapshot: it has no meaningful age, and showing
    // one made a fresh install claim its prices were a year old.
    if (h < 0) {
      btn.title = "Using bundled prices. Download the latest catalog for future usage.";
      btn.classList.remove("unpriced");
      return;
    }
    if (!(h > 0)) {
      btn.title = "Prices just updated. Download latest rates for future usage; history remains unchanged.";
      btn.classList.remove("unpriced");
      return;
    }
    const days = Math.floor(h / 24);
    const label = days >= 1 ? days + "d" : Math.max(1, Math.round(h)) + "h";
    btn.title = "Prices are " + label + " old. Download latest rates for future usage; history remains unchanged.";
    // A table older than a week is worth drawing attention to.
    btn.classList.toggle("unpriced", h > 24 * 7);
  }).catch(() => { /* backend not ready */ });
}

$("bd-unpriced").addEventListener("click", openPriceModal);
$("price-close").addEventListener("click", closePriceModal);
priceModal.querySelector(".modal-backdrop").addEventListener("click", closePriceModal);

// ---- cheaper models modal ----
const modal = $("model-modal");
const modelList = $("model-list");

// Strip only the provider; model IDs can themselves contain slashes.
function bareModel(key) {
  const i = String(key).indexOf("/");
  return i >= 0 ? String(key).slice(i + 1) : String(key);
}

// providerOf pulls the provider from a "provider/model" key.
function providerOf(key) {
  const i = String(key || "").indexOf("/");
  return i > 0 ? String(key).slice(0, i) : "";
}

// Route a choice through the plugin; budget settings stay unchanged.
async function copyModelCommand(m, sessionID) {
  if(!await chooseModel(m, sessionID))return;
  if(m.free){closeAdvice();toast("Free model selected. Continue in OpenCode; your limit stays on.","ok");}
  else if(snapshot?.budget_state==="over" && snapshot?.enforced)toast("Paid model selected. Increase the limit to continue.","warn");
}

function modelRow(m, onPick) {
  const card = document.createElement("div");
  card.className = "model-card";

  const info = document.createElement("div");
  info.className = "model-info";

  const title = document.createElement("div");
  title.className = "model-title";
  title.textContent = m.name || m.key;
  // The same model is sold by several providers at different prices, so the
  // display name alone is not enough to choose by.
  const prov = m.provider || providerOf(m.key);
  if (prov) {
    const p = document.createElement("span");
    p.className = "prov-tag";
    p.textContent = prov;
    title.appendChild(p);
  }
  const tag = document.createElement("span");
  tag.className = "tier-tag " + (m.free ? "free" : "silver");
  tag.textContent = m.tier || (m.free ? "FREE" : m.unknown ? "UNKNOWN" : m.cheaper ? "CHEAPER" : "PAID");
  title.appendChild(tag);

  const desc = document.createElement("div");
  desc.className = "model-desc";
  desc.textContent = m.description ||
    (m.free ? "Free - no budget impact"
            : Math.round((m.ratio || 0) * 100) + "% of current price at equal input/output tokens");

  const price = document.createElement("div");
  price.className = "model-price";
  const inC = m.input_cost !== undefined ? m.input_cost : m.input;
  const outC = m.output_cost !== undefined ? m.output_cost : m.output;
  price.textContent = m.free ? "FREE"
    : m.unknown ? "Price unknown" : (m.estimated ? "~ " : "") + "$" + (inC || 0).toFixed(2) + " in / $" + (outC || 0).toFixed(2) + " out  per 1M";

  info.appendChild(title);
  info.appendChild(desc);
  info.appendChild(price);

  const pick = document.createElement("button");
  pick.className = "model-pick-btn" + (m.free ? " free-btn" : "");
  // The next request is routed through the OpenCode plugin.
  const specialised=m.category && m.category!=="chat";
  pick.textContent = specialised ? "SPECIALISED" : m.chat_selected ? "SELECTED" : "SWITCH MODEL";
  pick.disabled=!!specialised;
  pick.title = specialised ? "This model cannot run a coding chat" : "Keep using " + m.key + " in this chat, starting next message";
  pick.addEventListener("click", (e) => { e.stopPropagation(); onPick(m); });

  card.appendChild(info);
  card.appendChild(pick);
  card.title=m.key + (m.source ? " · "+m.source : "");
  return card;
}

function openModelModal() { loadModelPicker(); }
function closeModelModal() { modal.classList.add("hidden"); }

$("bd-switch").addEventListener("click", openModelModal);
$("modal-close").addEventListener("click", closeModelModal);
modal.querySelector(".modal-backdrop").addEventListener("click", closeModelModal);

// ---- limit reached screen (the Python `show_advice` dialog) ----
const advice = $("advice");
let adviceOpen = false;
let adviceShownFor = null; // session id we already interrupted for
// Armed a few seconds after launch, so startup seeding cannot trigger the
// limit screen. See checkAdvice.
let adviceArmed = false;
setTimeout(() => { adviceArmed = true; }, 5000);
// True when the budget screen has taken the whole window over.
let adviceIsWindow = false;

// asWindow: take the whole window over (used when the budget is breached), so
// this behaves like the separate always-on-top dialog the Python build showed
// rather than a panel hidden behind the IDE.
function openAdvice(asWindow) {
  Svc().Advice().then((a) => {
    if (!a) return;
    adviceOpen = true;

    if (asWindow) {
      adviceIsWindow = true;
      document.body.classList.add("alert-mode");
      $("bar").classList.add("hidden");
      $("board").classList.add("hidden");
      Svc().ShowAlert();
    }

    const hard = snapshot && snapshot.past_hard_stop;
    const over = snapshot && snapshot.budget_state === "over";
    $("advice-title").textContent = hard ? "HARD STOP REACHED"
      : over ? "SESSION LIMIT REACHED" : "SESSION BUDGET";
    setAdviceLimit(a.limit || (snapshot ? snapshot.limit : 0));

    // Volume behind the spend: "$5 over 400k tokens and 60 turns" is a very
    // different decision from "$5 in three turns".
    $("astat-cost").textContent = "$" + (a.cost || 0).toFixed(4);
    $("astat-rate").textContent = "$" + (a.rate || 0).toFixed(2) + "/hr";
    $("astat-rate").style.color = (a.rate || 0) > 5 ? "var(--red)" : "var(--text-mid)";
    $("astat-tokens").textContent = human(a.tokens || 0);
    $("astat-msgs").textContent = String(a.msgs || 0);
    $("advice-sub").textContent =
      `This session has spent $${(a.cost || 0).toFixed(4)} of its $${a.limit} limit. ` +
      (!over
        ? "Nothing is blocked yet."
        : snapshot && snapshot.enforced
        ? "Paid turns are blocked. Choose a free model to continue, or increase the limit."
        : "Enforcement is off, so nothing is being blocked.");

    const dv = $("advice-drivers");
    dv.innerHTML = "";
    if (!a.drivers || !a.drivers.length) {
      const d = document.createElement("div");
      d.className = "driver-row";
      d.textContent = "(no priced spend recorded for this session)";
      dv.appendChild(d);
    } else {
      for (const d of a.drivers) {
        const row = document.createElement("div");
        row.className = "driver-row";
        const n = document.createElement("span");
        n.textContent = d.key;
        const v = document.createElement("span");
        v.className = "driver-cost";
        v.textContent = "$" + (d.output || 0).toFixed(4);
        row.appendChild(n);
        row.appendChild(v);
        // Not clickable: a driver is information about where the money went,
        // and copying its name gives the user nothing to do. The actionable
        // choice is the cheaper-model list below.
        row.title = d.key;
        dv.appendChild(row);
      }
    }

    const ch = $("advice-cheaper");
    ch.innerHTML = "";
    if (!a.cheaper || !a.cheaper.length) {
      const d = document.createElement("div");
      d.className = "modal-sub";
      d.textContent = "No known cheaper option. Open All configured to see unknown prices.";
      ch.appendChild(d);
    } else {
      for (const m of a.cheaper) {
        ch.appendChild(modelRow(m, (x) => copyModelCommand(x, a.session_id)));
      }
    }

    advice.classList.remove("hidden");
  });
}

function closeAdvice() {
  advice.classList.add("hidden");
  adviceOpen = false;
  if (adviceIsWindow) {
    adviceIsWindow = false;
    document.body.classList.remove("alert-mode");
    // Restore whichever layout was in use before the alert took over. The
    // backend owns that decision, so ask it and follow.
    Svc().DismissAlert().then(() => Svc().Snapshot()).then((snap) => {
      if (!snap) return;
      const toBoard = !snap.compact;
      $("bar").classList.toggle("hidden", toBoard);
      $("board").classList.toggle("hidden", !toBoard);
      render(snap);
    }).catch(() => { /* restore is best effort */ });
  }
}

$("advice-close").addEventListener("click", closeAdvice);
advice.querySelector(".modal-backdrop").addEventListener("click", closeAdvice);

// Nudge the limit in sensible steps rather than only doubling: most of the
// time you want "a bit more", not 2x.
function limitStep(v) {
  if (v < 1) return 0.25;
  if (v < 5) return 0.5;
  if (v < 20) return 1;
  if (v < 100) return 5;
  return 25;
}
function adviceLimitValue() {
  const v = parseFloat($("advice-limit-input").value);
  return isNaN(v) ? (snapshot ? snapshot.limit : 0) : v;
}
function setAdviceLimit(v) {
  $("advice-limit-input").value = String(Math.max(0, Math.round(v * 100) / 100));
}
function applyAdviceLimit() {
  const v = adviceLimitValue();
  if (v <= 0) { toast("Limit must be greater than 0", "over"); return; }
  Svc().SetLimit(v);
  closeAdvice();
  toast("Limit set to $" + v, "ok");
}

$("advice-dec").addEventListener("click", () => {
  const v = adviceLimitValue();
  setAdviceLimit(Math.max(0, v - limitStep(v)));
});
$("advice-inc").addEventListener("click", () => {
  const v = adviceLimitValue();
  setAdviceLimit(v + limitStep(v));
});
$("advice-apply").addEventListener("click", applyAdviceLimit);
$("advice-limit-input").addEventListener("keydown", (e) => {
  if (e.key === "Enter") applyAdviceLimit();
});

$("advice-allow").addEventListener("click", () => {
  const limit = snapshot ? snapshot.limit : 0;
  Svc().AllowMoreSession(0, limit)
    .then(() => { closeAdvice(); toast("Allowed $" + limit + " more for this session", "ok"); })
    .catch((err) => toast("Failed: " + err, "over"));
});
$("advice-double").addEventListener("click", () => {
  Svc().RaiseLimit();
  closeAdvice();
  toast("Limit doubled", "ok");
});
$("advice-stop").addEventListener("click",()=>stopSession(true));
$("advice-disable").addEventListener("click", () => {
  Svc().SetEnabled(false);
  closeAdvice();
  toast("Enforcement turned off", "ok");
});

// Fire the advice screen on the crossing into "over", but only when something
// is actually blocked: in warn mode nothing is refused, so a modal is noise.
function checkAdvice(snap) {
	// Open-session breaches remain actionable inline in the dock/panel.
	if(Array.isArray(snap.open_sessions))return;
  // Ignore the first snapshots after launch. Startup seeding briefly reports
  // a session as over before baselines are rebased, and interrupting with a
  // modal (which also force-expands the window) on every start is wrong: the
  // screen should appear when you cross the limit, not when you open the app.
  if (!adviceArmed) return;

  if (!snap.budget_enabled || !snap.enforced || snap.preferences?.paused || snap.budget_state !== "over") {
    if (snap.budget_state !== "over") adviceShownFor = null;
    return;
  }
  const sid = snap.session_id || "";
  if (adviceOpen || adviceShownFor === sid) return;
  adviceShownFor = sid;
  // Breaching the limit opens the budget screen as its own window, centred
  // and above other apps, so the decision cannot be missed behind the editor.
  openAdvice(true);
}

// ---- right-click context menu (the Python bar menu) ----
const menu = $("menu");

function menuItem(label, fn, cls) {
  const el = document.createElement("div");
  el.className = "ctxitem" + (cls ? " " + cls : "");
  el.textContent = label;
  el.addEventListener("click", () => { closeMenu(); fn(); });
  return el;
}
function menuSep() {
  const el = document.createElement("div");
  el.className = "ctxsep";
  return el;
}

function openMenu(x, y) {
  menu.innerHTML = "";
  const expanded = $("board").classList.contains("hidden") === false;

  menu.appendChild(menuItem(expanded ? "Collapse" : "Expand", () => setView(!expanded)));
  menu.appendChild(menuSep());

  Svc().Docks().then((docks) => {
    const cur = snapshot ? snapshot.dock : "";
    for (const d of docks || []) {
      const item = menuItem((d === cur ? "\u2713 " : "   ") + "Dock: " + d,
        () => Svc().SetDock(d));
      menu.appendChild(item);
    }
    menu.appendChild(menuItem("Reset position (bottom centre)",
      () => Svc().SetDock("bottom-center").then(() => toast("Docked bottom-centre", "ok"))));
    menu.appendChild(menuSep());
    for(const view of ["RUN","TRIP","TOTAL"])menu.appendChild(menuItem("Show "+view, () => Svc().SetCounter(view)));
    if(snapshot?.view==="TRIP")menu.appendChild(menuItem("Reset trip", () => {
      if (confirm("Reset the trip counter?")) Svc().ResetTrip();
    }));
    menu.appendChild(menuItem("Switch model", openModelModal));
    menu.appendChild(menuItem("Settings…", openSettings));
    if(!Array.isArray(snapshot?.open_sessions))menu.appendChild(menuItem(snapshot?.preferences?.remaining ? "Show spent" : "Show remaining",toggleReading));
    // Opened deliberately, so show it inline rather than seizing the window.
    menu.appendChild(menuItem("Budget details\u2026", () => Array.isArray(snapshot?.open_sessions)?expandSessions():openAdvice(false)));
    menu.appendChild(menuSep());
    menu.appendChild(menuItem("Hide to tray", () => {cancelPeek();Svc().HideToTray();}));
    menu.appendChild(menuItem("Quit", () => {
      if (confirm("Quit the odometer? Spend tracking stops until you start it again."))
        Svc().Quit();
    }, "danger"));

    // Keep the menu on screen.
    menu.classList.remove("hidden");
    const r = menu.getBoundingClientRect();
    const nx = Math.min(x, window.innerWidth - r.width - 4);
    const ny = Math.min(y, window.innerHeight - r.height - 4);
    menu.style.left = Math.max(2, nx) + "px";
    menu.style.top = Math.max(2, ny) + "px";
  });
}
function closeMenu() { menu.classList.add("hidden"); }

window.addEventListener("contextmenu", (e) => {
  e.preventDefault();
  openMenu(e.clientX, e.clientY);
});
window.addEventListener("click", (e) => {
  if (!menu.contains(e.target)) closeMenu();
});
window.addEventListener("blur", closeMenu);

// ---- budget warning toast (fires once per threshold) ----
let toastWarned = false, toastOvered = false;
function checkToast(snap) {
  if(Array.isArray(snap.open_sessions))return;
  if (snap.preferences?.paused) return;
  if (!snap.budget_enabled || snap.limit <= 0) { toastWarned = toastOvered = false; return; }
  if (snap.budget_state === "over") {
    if (!toastOvered) {
      toastOvered = true;
      toast("Budget exceeded - $" + snap.session_cost.toFixed(3), "over");
    }
    toastWarned = true;
  } else if (snap.budget_state === "warn") {
    if (!toastWarned) {
      toastWarned = true;
      toast("Budget warning at $" + snap.session_cost.toFixed(3), "warn");
    }
    toastOvered = false;
  } else {
    toastWarned = false;
    toastOvered = false;
  }
}
let toastTimer;
function toast(msg, kind) {
  const t = $("toast");
  t.textContent = msg;
  t.className = "toast" + (kind === "over" ? " over" : kind === "ok" ? " ok" : "");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.add("hidden"), 3500);
}

// ---- keyboard shortcuts (match the Python bindings) ----
window.addEventListener("keydown", (e) => {
  if (e.key === "Escape") {
    if (!menu.classList.contains("hidden")) closeMenu();
    else if (!advice.classList.contains("hidden")) closeAdvice();
    else if (!priceModal.classList.contains("hidden")) closePriceModal();
    else if (!modal.classList.contains("hidden")) closeModelModal();
    else if (!settingsModal.classList.contains("hidden")) closeSettings();
    else setView(false);
  } else if (e.key === "F2") {
    Svc().CycleDock();
  }
});

// Double click the compact bar to expand.
$("bar").addEventListener("dblclick", () => setView(true));

// Single click the compact odometer toggles TRIP / TOTAL (delayed so a
// double click expands instead of toggling twice).
let clickTimer = null;
$("bar-odometer").addEventListener("click", () => {
  if (clickTimer) return;
  clickTimer = setTimeout(() => {
    clickTimer = null;
    if (!$("bar").classList.contains("hidden")) expandSessions();
  }, 220);
});
$("bar-odometer").addEventListener("dblclick", () => {
  clearTimeout(clickTimer);
  clickTimer = null;
});

// Persist the window position after a real drag. Wails moves the frameless
// window natively via CSS drag, but nothing recorded where it ended up, so the
// bar jumped back to its dock on every restart.
//
// Only an actual move counts. Treating every click on a draggable region as a
// drag switched the widget to "floating" the first time you clicked the bar,
// which is what stopped it docking to bottom-centre.
const DRAG_THRESHOLD = 4; // px
let dragOrigin = null;

document.addEventListener("mousedown", (e) => {
  if (e.button === 0 && e.target.closest(".drag")) {
    dragOrigin = { x: e.screenX, y: e.screenY };
  }
});
document.addEventListener("mouseup", (e) => {
  const origin = dragOrigin;
  dragOrigin = null;
  if (!origin) return;

  const moved = Math.abs(e.screenX - origin.x) > DRAG_THRESHOLD ||
                Math.abs(e.screenY - origin.y) > DRAG_THRESHOLD;
  if (!moved) return; // a click, not a drag - keep the current dock

  if (window.runtime && window.runtime.WindowGetPosition) {
    Promise.resolve(window.runtime.WindowGetPosition()).then((p) => {
      if (p && typeof p.x === "number") Svc().SavePosition(p.x, p.y);
    }).catch(() => { /* position is best effort */ });
  }
});

let eventsHooked = false;
function hookEvents() {
  if (eventsHooked) return;
  if (window.runtime && window.runtime.EventsOn) {
    window.runtime.EventsOn("refresh", (snap) => { if (snap) render(snap); });
    eventsHooked = true;
  }
}

// ---- wiring to backend events ----
async function initApp() {
  try {
    hookEvents();
    if (window.go && window.go.wservice && window.go.wservice.Service) {
      await Svc().SetScreenSize(window.screen.width, window.screen.height);
      const snap = await Svc().Snapshot();
      if (snap) {
        render(snap);
        // Show the layout the user left, rather than forcing compact and
        // discarding the restored state.
        const toBoard = !snap.compact;
        $("bar").classList.toggle("hidden", toBoard);
        $("board").classList.toggle("hidden", !toBoard);
        await Svc().SetCompact(snap.compact);
      }
      refreshUnpriced();
      refreshPricesAge();
      // New models appear as they are used, so re-check periodically. Cheap:
      // it reads a map the backend already maintains.
      setInterval(refreshUnpriced, 60_000);
      // Every minute, not every half hour: the background fetch finishes about
      // 20s after startup, and a slow poll left the button advertising a stale
      // age long after the table had been replaced.
      setInterval(refreshPricesAge, 60_000);
    }
  } catch (err) {
    console.warn("initApp wait:", err);
  }
}

window.addEventListener("DOMContentLoaded", () => {
  paintSortArrows();
  initApp();

  // The backend pushes a "refresh" event on every change, so this is only a
  // safety net for a missed event / late binding - not the primary path.
  setInterval(async () => {
    try {
      hookEvents();
      if (eventsHooked) return;
      if (window.go && window.go.wservice && window.go.wservice.Service) {
        const snap = await Svc().Snapshot();
        if (snap) render(snap);
      }
    } catch (e) {
      /* backend not ready yet */
    }
  }, 1000);
});
