"""
OpenCode Odometer
=================
Polls the live OpenCode SQLite database once per second, prices every
assistant message from a local authoritative price table, and shows the
running spend on a mechanical-odometer display.

Data source : ~/.local/share/opencode/opencode.db  (table `message`, JSON `data`)
Price source: prices.json next to this script (fully editable, offline)

Free / sovereign models are detected automatically and never add to cost,
but their tokens are still counted and their "would have cost" shown as savings.

Runs in two shapes:
  COMPACT  - small frameless odometer bar, docked bottom-center / top-right
  EXPANDED - full window with per-model breakdown, controls and CSV export
Click the bar (or press the chevron) to switch. Drag it anywhere.
Only one instance can run at a time.
"""

import csv
import json
import os
import shutil
import sqlite3
import sys
import tempfile
import tkinter as tk
from datetime import datetime
from tkinter import messagebox, ttk

# ==========================================================================
# PATHS / CONSTANTS
# ==========================================================================

def app_dir():
    if getattr(sys, "frozen", False):
        return os.path.dirname(sys.executable)
    return os.path.dirname(os.path.abspath(__file__))


def data_dir():
    """Writable location for state/budget/config.

    Deliberately NOT next to the executable: an app installed under
    Program Files cannot write beside itself. Override with
    OPENCODE_ODOMETER_DIR.
    """
    env = os.environ.get("OPENCODE_ODOMETER_DIR")
    if env:
        d = os.path.expanduser(env)
    elif os.name == "nt":
        base = os.environ.get("LOCALAPPDATA") or os.path.expanduser("~")
        d = os.path.join(base, "OpenCodeOdometer")
    else:
        base = (os.environ.get("XDG_DATA_HOME")
                or os.path.join(os.path.expanduser("~"), ".local", "share"))
        d = os.path.join(base, "opencode-odometer")
    try:
        os.makedirs(d, exist_ok=True)
    except OSError:
        d = app_dir()  # last resort: run-in-place
    return d


APP_DIR = app_dir()
DATA_DIR = data_dir()

# A tiny, stable pointer file in a fixed location. The plugin reads this to
# find wherever the odometer actually keeps its data, so moving DATA_DIR (or
# setting OPENCODE_ODOMETER_DIR) can never leave the plugin reading a stale
# ledger from an old path.
POINTER_FILE = os.path.join(os.path.expanduser("~"), ".opencode-odometer.json")

# Read-only assets ship with the app; writable data lives in DATA_DIR.
# A prices.json beside the exe still wins, so a portable copy keeps working.
PRICES_FILE = (os.path.join(APP_DIR, "prices.json")
               if os.path.exists(os.path.join(APP_DIR, "prices.json"))
               else os.path.join(DATA_DIR, "prices.json"))
PRICES_LOCAL = os.path.join(DATA_DIR, "prices.local.json")
CONFIG_FILE = os.path.join(DATA_DIR, "config.json")
STATE_FILE = os.path.join(DATA_DIR, "odometer_state.json")
BUDGET_FILE = os.path.join(DATA_DIR, "budget.json")
# The plugin appends grace claims here; the odometer folds them into state.
# Separate file so budget.json keeps a single writer (the odometer).
GRACE_FILE = os.path.join(DATA_DIR, "grace_claims.json")
LOCK_FILE = os.path.join(tempfile.gettempdir(), "opencode_odometer.lock")


def write_pointer():
    """Advertise where our data lives so the plugin never has to guess."""
    try:
        tmp = POINTER_FILE + ".tmp"
        with open(tmp, "w", encoding="utf-8") as f:
            json.dump({
                "_comment": "Written by OpenCode Odometer so the plugin can "
                            "locate budget.json. Safe to delete.",
                "data_dir": DATA_DIR,
                "budget": BUDGET_FILE,
                "grace": GRACE_FILE,
                "app_dir": APP_DIR,
                "pid": os.getpid(),
                "updated": int(datetime.now().timestamp()),
            }, f, indent=2)
        os.replace(tmp, POINTER_FILE)
    except Exception as e:
        print(f"[pointer] write failed: {e}")


def _default_db():
    """Locate opencode.db across platforms; override with OPENCODE_DB."""
    env = os.environ.get("OPENCODE_DB")
    if env:
        return os.path.expanduser(env)
    home = os.path.expanduser("~")
    candidates = [
        os.path.join(home, ".local", "share", "opencode", "opencode.db"),
        os.path.join(os.environ.get("XDG_DATA_HOME", ""), "opencode",
                     "opencode.db") if os.environ.get("XDG_DATA_HOME") else "",
        os.path.join(home, "Library", "Application Support", "opencode",
                     "opencode.db"),
        os.path.join(os.environ.get("APPDATA", ""), "opencode", "opencode.db")
        if os.environ.get("APPDATA") else "",
    ]
    for c in candidates:
        if c and os.path.exists(c):
            return c
    return candidates[0]


OPENCODE_DB = _default_db()

# Maximum priced messages retained in odometer_state.json. Oldest are dropped
# first; totals for pruned messages are folded into a carried-forward base so
# the lifetime figure stays correct.
MAX_MESSAGES = int(os.environ.get("OPENCODE_ODOMETER_MAX_MESSAGES", "20000"))

POLL_MS = 1000  # one second

BG = "#0b0b0d"
PANEL = "#141417"
EDGE = "#27272a"
DIM = "#71717a"
GREEN = "#22c55e"
GREEN_DIM = "#14532d"
GREEN_GLOW = "#16a34a"
RED = "#ef4444"
RED_DIM = "#7f1d1d"
AMBER = "#f59e0b"
AMBER_DIM = "#78350f"
TEXT_HI = "#fafafa"
TEXT_MID = "#d4d4d8"
TEXT_LO = "#a1a1aa"

# Digit cell colours
CELL_WHOLE_BG = "#1a1a1f"
CELL_WHOLE_BORDER = "#3f3f46"
CELL_WHOLE_HILITE = "#2a2a30"   # top inner highlight for bevel
CELL_FRAC_BG = "#e4e4e7"
CELL_FRAC_BORDER = "#a1a1aa"
CELL_FRAC_HILITE = "#f4f4f5"

DOCKS = ("bottom-center", "top-right", "top-center", "bottom-right")

BAR_W, BAR_H = 360, 46      # compact bar
FULL_W, FULL_H = 620, 560   # expanded window
TASKBAR = 56                # keep the bar clear of the Windows taskbar


# ==========================================================================
# CANVAS HELPERS
# ==========================================================================

def lighten(hexcol, amount=0.2):
    """Blend a #rrggbb colour towards white. Used for button hover states."""
    try:
        h = hexcol.lstrip("#")
        r, g, b = (int(h[i:i + 2], 16) for i in (0, 2, 4))
    except (ValueError, IndexError):
        return hexcol
    r = min(255, int(r + (255 - r) * amount))
    g = min(255, int(g + (255 - g) * amount))
    b = min(255, int(b + (255 - b) * amount))
    return f"#{r:02x}{g:02x}{b:02x}"


def rounded_rect(canvas, x1, y1, x2, y2, r, **kw):
    """Draw a rounded rectangle on a tk Canvas using arcs + rectangle fill.

    `r` is the corner radius. All extra kwargs go to create_polygon.
    """
    pts = [
        x1 + r, y1,
        x2 - r, y1,
        x2, y1,
        x2, y1 + r,
        x2, y2 - r,
        x2, y2,
        x2 - r, y2,
        x1 + r, y2,
        x1, y2,
        x1, y2 - r,
        x1, y1 + r,
        x1, y1,
    ]
    return canvas.create_polygon(pts, smooth=True, **kw)


def beveled_cell(canvas, x, y, w, h, bg, border, hilite):
    """Draw a single digit cell with a subtle top-edge bevel highlight."""
    canvas.create_rectangle(x, y, x + w, y + h, fill=bg, outline=border)
    # inner top highlight — gives a subtle 3D lift
    canvas.create_line(x + 1, y + 1, x + w - 1, y + 1, fill=hilite)


# ==========================================================================
# SINGLE INSTANCE
# ==========================================================================

def acquire_lock():
    """Return True if we are the only instance."""
    try:
        if os.path.exists(LOCK_FILE):
            with open(LOCK_FILE) as f:
                pid = int((f.read() or "0").strip() or 0)
            if pid and pid_alive(pid):
                return False
        with open(LOCK_FILE, "w") as f:
            f.write(str(os.getpid()))
        return True
    except Exception:
        return True


def pid_alive(pid):
    try:
        import ctypes
        h = ctypes.windll.kernel32.OpenProcess(0x1000, False, pid)
        if not h:
            return False
        ctypes.windll.kernel32.CloseHandle(h)
        return True
    except Exception:
        return False


def release_lock():
    try:
        if os.path.exists(LOCK_FILE):
            os.remove(LOCK_FILE)
    except Exception:
        pass


# ==========================================================================
# PRICING
# ==========================================================================

class PriceBook:
    """prices.json is authoritative. USD per 1,000,000 tokens."""

    ZERO = {"input": 0.0, "output": 0.0, "cache_read": 0.0, "cache_write": 0.0}

    # Fallback benchmark for valuing free-model usage, used only when the
    # price file does not declare its own `reference_model`.
    FALLBACK_REFERENCE = "anthropic/claude-sonnet-4-5"

    def __init__(self, path, overlay=None):
        self.path = path
        self.overlay = overlay
        self.models = {}
        self.reference_model = self.FALLBACK_REFERENCE
        self.unknown = set()
        self.load()

    def load(self):
        self.models = {}
        try:
            with open(self.path, "r", encoding="utf-8") as f:
                doc = json.load(f)
            self.models = doc.get("models", {})
            self.reference_model = (doc.get("reference_model")
                                    or self.FALLBACK_REFERENCE)
        except Exception as e:
            print(f"[prices] load failed {self.path}: {e}")

        # Optional private overlay (reseller rates, internal gateways).
        # Merged on top so regenerating prices.json never clobbers it.
        if self.overlay and os.path.exists(self.overlay):
            try:
                with open(self.overlay, "r", encoding="utf-8") as f:
                    doc = json.load(f)
                extra = doc.get("models", {})
                self.models.update(extra)
                if doc.get("reference_model"):
                    self.reference_model = doc["reference_model"]
                print(f"[prices] overlay: +{len(extra)} models from "
                      f"{os.path.basename(self.overlay)}")
            except Exception as e:
                print(f"[prices] overlay load failed {self.overlay}: {e}")

        # A reference that does not exist would silently zero out savings.
        if self.reference_model not in self.models:
            alt = next((k for k in (self.FALLBACK_REFERENCE,) if k in self.models),
                       None)
            if alt:
                self.reference_model = alt
            elif self.models:
                # cheapest sane paid model as a last resort
                paid = [(k, v) for k, v in self.models.items()
                        if not v.get("free") and float(v.get("output") or 0) > 0]
                if paid:
                    self.reference_model = max(
                        paid, key=lambda kv: float(kv[1].get("output") or 0))[0]

    @staticmethod
    def looks_free(key):
        low = key.lower()
        return low.endswith("-free") or "sovereign" in low or "-free-" in low

    def entry(self, key):
        e = self.models.get(key)
        if e is not None:
            return e
        if self.looks_free(key):
            return {"free": True, "sovereign": "sovereign" in key.lower(),
                    "name": key, **self.ZERO}
        self.unknown.add(key)
        return {"free": False, "unknown": True, "name": key, **self.ZERO}

    def is_free(self, key):
        return bool(self.entry(key).get("free"))

    def cost_of(self, key, tin, tout, cread, cwrite):
        e = self.entry(key)
        if e.get("free"):
            return 0.0
        return (tin / 1e6 * float(e.get("input") or 0)
                + tout / 1e6 * float(e.get("output") or 0)
                + cread / 1e6 * float(e.get("cache_read") or 0)
                + cwrite / 1e6 * float(e.get("cache_write") or 0))

    def shadow_cost(self, key, tin, tout, cread, cwrite):
        """What a free model would have cost at the paid reference rate."""
        ref = self.models.get(self.reference_model)
        if not ref:
            return 0.0
        return (tin / 1e6 * float(ref.get("input") or 0)
                + tout / 1e6 * float(ref.get("output") or 0)
                + cread / 1e6 * float(ref.get("cache_read") or 0)
                + cwrite / 1e6 * float(ref.get("cache_write") or 0))


# ==========================================================================
# BUDGET
# ==========================================================================

class Budget:
    """Per-session spend limit, shared with the OpenCode plugin via budget.json.

    The Odometer owns this file: it writes live per-session costs and the
    over/under verdict. The plugin only reads it and blocks turns. Keeping
    the decision here means the plugin stays trivial and cannot mis-price.

    Enforcement is tiered rather than binary, because "slightly over on a task
    I am about to finish" and "an agent loop is burning money unattended" are
    different problems:

        warn  - never blocks; toast only (pure visibility)
        soft  - blocks at 100%, but grants ONE grace turn first,
                then hard-stops at `hard_stop_at` x limit   [default]
        hard  - blocks at 100% immediately, no grace

    Auto-switching to a cheaper model is deliberately NOT offered: the plugin
    API exposes the model as read-only on `chat.message`, and silently
    degrading quality mid-task produces confusing output. We advise instead.
    """

    MODES = ("warn", "soft", "hard")

    DEFAULTS = {
        "enabled": False,
        "session_limit_usd": 5.0,
        "warn_at_percent": 80,
        "block_when_exceeded": True,
        "mode": "soft",
        # multiple of the limit at which even `soft` refuses to grant grace
        "hard_stop_at": 1.5,
        # how many one-shot grace turns a session gets on first breach
        "grace_turns": 1,
    }

    def __init__(self, path):
        self.path = path
        self.cfg = dict(self.DEFAULTS)
        self.load()

    def load(self):
        try:
            with open(self.path, "r", encoding="utf-8") as f:
                disk = json.load(f)
            for k in self.DEFAULTS:
                if k in disk:
                    self.cfg[k] = disk[k]
        except Exception:
            pass

    def save(self, sessions=None):
        """Persist settings plus the live per-session ledger."""
        doc = {
            "_comment": ("Budget enforcement for OpenCode. Written by the "
                         "Odometer UI, read by the OpenCode plugin."),
            **self.cfg,
            "_runtime_comment": "Maintained by the Odometer. Do not edit by hand.",
            "sessions": sessions or {},
            "updated": int(datetime.now().timestamp()),
        }
        tmp = self.path + ".tmp"
        try:
            with open(tmp, "w", encoding="utf-8") as f:
                json.dump(doc, f, indent=2)
            os.replace(tmp, self.path)   # atomic: plugin never sees half a file
        except Exception as e:
            print(f"[budget] save failed: {e}")

    @property
    def enabled(self):
        return bool(self.cfg["enabled"])

    @property
    def limit(self):
        return float(self.cfg["session_limit_usd"] or 0)

    @property
    def mode(self):
        m = str(self.cfg.get("mode") or "soft").lower()
        return m if m in self.MODES else "soft"

    @property
    def hard_stop_at(self):
        try:
            return max(1.0, float(self.cfg.get("hard_stop_at") or 1.5))
        except (TypeError, ValueError):
            return 1.5

    @property
    def grace_turns(self):
        if self.mode != "soft":
            return 0
        try:
            return max(0, int(self.cfg.get("grace_turns") or 0))
        except (TypeError, ValueError):
            return 0

    def blocks(self):
        """Does this configuration ever refuse a turn?"""
        return (self.enabled and self.mode != "warn"
                and bool(self.cfg.get("block_when_exceeded", True)))

    def status(self, cost):
        """-> ('ok'|'warn'|'over', fraction_of_limit)

        This is a pure MEASUREMENT and is deliberately independent of
        `enabled`. A disabled limit used to report ("ok", 0.0) for every
        session no matter how much had been spent, which made the ledger
        actively misleading to read and left the UI showing 0% for a poll
        after re-enabling.

        Enforcement is a separate question - see `blocks()`. Callers that
        act on this must gate on `blocks()` (refusing turns) or `enabled`
        (colouring the UI); they must not infer either from the state.
        """
        if self.limit <= 0:
            return "ok", 0.0
        frac = cost / self.limit
        if frac >= 1.0:
            return "over", frac
        if frac >= float(self.cfg["warn_at_percent"]) / 100.0:
            return "warn", frac
        return "ok", frac


# ==========================================================================
# OPENCODE DATABASE READER
# ==========================================================================

class OpenCodeDB:
    """Read-only snapshot reader for the live WAL-mode OpenCode database."""

    def __init__(self, src):
        self.src = src
        self.tmp = os.path.join(tempfile.gettempdir(),
                                "opencode_odometer_snapshot.db")

    def available(self):
        return os.path.exists(self.src)

    def mtime(self):
        newest = 0
        for ext in ("", "-wal"):
            p = self.src + ext
            if os.path.exists(p):
                newest = max(newest, os.path.getmtime(p))
        return newest

    def _snapshot(self):
        # -shm is a scratch file; SQLite rebuilds it. Copying it can fail
        # while OpenCode holds it, so it is best-effort only.
        for ext in ("", "-wal", "-shm"):
            s, d = self.src + ext, self.tmp + ext
            try:
                if os.path.exists(s):
                    shutil.copyfile(s, d)
                elif os.path.exists(d):
                    os.remove(d)
            except OSError:
                if ext == "":
                    raise            # main db must copy
                try:
                    os.remove(d)     # drop stale sidecar
                except OSError:
                    pass

    def fetch_since(self, last_updated):
        """Rows whose time_updated >= watermark.

        OpenCode inserts an assistant row immediately with zero tokens and
        fills the real usage in seconds later (measured: 343 of 346 rows are
        updated after insert, lagging 1.5-19s). So we must follow
        time_updated, not time_created, or every message is counted as zero.
        """
        self._snapshot()
        con = sqlite3.connect(f"file:{self.tmp}?mode=ro", uri=True)
        try:
            rows = con.execute(
                """SELECT id, session_id, time_created, time_updated, data
                   FROM message
                   WHERE time_updated >= ?
                   ORDER BY time_updated ASC, id ASC""",
                (last_updated,)).fetchall()
        finally:
            con.close()

        out = []
        for mid, sid, tc, tu, data in rows:
            try:
                d = json.loads(data)
            except Exception:
                continue
            if d.get("role") != "assistant":
                continue
            tok = d.get("tokens") or {}
            ca = tok.get("cache") or {}
            out.append((mid, sid, tc, tu, {
                "provider": d.get("providerID") or "?",
                "model": d.get("modelID") or "?",
                "in": int(tok.get("input") or 0),
                "out": int(tok.get("output") or 0),
                "reasoning": int(tok.get("reasoning") or 0),
                "cache_read": int(ca.get("read") or 0),
                "cache_write": int(ca.get("write") or 0),
                "finished": bool(d.get("time", {}).get("completed")),
            }))
        return out


# ==========================================================================
# APP
# ==========================================================================

class OpenCodeOdometer(tk.Tk):

    def __init__(self):
        super().__init__()
        self.withdraw()

        self.prices = PriceBook(PRICES_FILE, overlay=PRICES_LOCAL)
        self.budget = Budget(BUDGET_FILE)
        self.db = OpenCodeDB(OPENCODE_DB)
        write_pointer()   # tell the plugin where to find our ledger

        self.state_data = {
            "total_cost": 0.0, "trip_cost": 0.0,
            "total_saved": 0.0, "trip_saved": 0.0,
            "trip_base": 0.0, "trip_saved_base": 0.0,
            "last_updated": 0,
            "per_model": {}, "per_session": {}, "messages": {},
            "budget_allow": {}, "budget_baselines": {},
            "grace_used": {},
            "pruned_cost": 0.0, "pruned_saved": 0.0,
            "seeded": False,
            "compact": True, "dock": "bottom-center",
            "pos": None, "view": "TRIP",
        }
        self.load_state()
        self.messages = self.state_data.get("messages") or {}

        self.view_mode = self.state_data.get("view", "TRIP")
        self.compact = bool(self.state_data.get("compact", True))
        self.dock = self.state_data.get("dock", "bottom-center")

        self._rate_window = []
        self._last_activity = None
        self._last_model = None
        self._pulse = 0
        self._db_mtime = 0
        self._drag = None

        self.title("OpenCode Odometer")
        self.configure(bg=BG)
        self.geometry(f"{BAR_W}x{BAR_H}+-2000+-2000")  # offscreen, avoids flash
        self.protocol("WM_DELETE_WINDOW", self.on_close)

        self.build_compact()
        self.build_expanded()
        self.apply_shape()
        self.deiconify()

        if not self.state_data["seeded"]:
            self.seed_history()
        else:
            self.refresh_table()

        self.bind("<Escape>", lambda e: self.set_compact(True))
        self.bind("<F2>", lambda e: self.cycle_dock())
        self.tick()

    # ---------------- persistence ----------------

    def load_state(self):
        if os.path.exists(STATE_FILE):
            try:
                with open(STATE_FILE, "r", encoding="utf-8") as f:
                    self.state_data.update(json.load(f))
            except Exception as e:
                print(f"[state] {e}")

    def prune_messages(self):
        """Cap the message store, folding dropped spend into a carried base.

        Without this the state file grows forever (1.6 MB at ~5.5k messages).
        Pruned messages still count towards lifetime totals via
        `pruned_cost` / `pruned_saved`, so the odometer never rolls backwards.
        """
        n = len(self.messages)
        if n <= MAX_MESSAGES:
            return 0
        # oldest first by recorded timestamp
        ordered = sorted(self.messages.items(),
                         key=lambda kv: kv[1].get("timestamp", ""))
        drop = ordered[:n - MAX_MESSAGES]
        for mid, rec in drop:
            self.state_data["pruned_cost"] = (
                self.state_data.get("pruned_cost", 0.0) + rec.get("cost", 0.0))
            self.state_data["pruned_saved"] = (
                self.state_data.get("pruned_saved", 0.0) + rec.get("saved", 0.0))
            self.messages.pop(mid, None)
        print(f"[state] pruned {len(drop)} old messages (cap {MAX_MESSAGES})")
        return len(drop)

    def save_state(self):
        try:
            self.prune_messages()
            self.state_data["compact"] = self.compact
            self.state_data["dock"] = self.dock
            self.state_data["view"] = self.view_mode
            self.state_data["messages"] = self.messages
            # atomic: a crash mid-write must not destroy the ledger
            tmp = STATE_FILE + ".tmp"
            with open(tmp, "w", encoding="utf-8") as f:
                json.dump(self.state_data, f)
            os.replace(tmp, STATE_FILE)
        except Exception as e:
            print(f"[state] save {e}")

    def on_close(self):
        self.save_state()
        release_lock()
        self.destroy()

    # ---------------- compact bar ----------------

    def build_compact(self):
        self.bar = tk.Frame(self, bg="#000000", highlightthickness=1,
                            highlightbackground=EDGE)

        self.bar_canvas = tk.Canvas(self.bar, width=226, height=36, bg="#000000",
                                    highlightthickness=0, cursor="fleur")
        self.bar_canvas.pack(side=tk.LEFT, padx=(7, 4), pady=3)

        right = tk.Frame(self.bar, bg="#000000")
        right.pack(side=tk.LEFT, fill=tk.Y, pady=3)

        self.bar_rate = tk.Label(right, text="$0.00/hr", fg=DIM, bg="#000000",
                                 font=("Consolas", 8, "bold"))
        self.bar_rate.pack(anchor="w")
        self.bar_model = tk.Label(right, text="idle", fg=DIM, bg="#000000",
                                  font=("Consolas", 8))
        self.bar_model.pack(anchor="w")

        self.bar_btn = tk.Label(self.bar, text="\u25b2", fg=DIM, bg="#000000",
                                font=("Segoe UI", 9), cursor="hand2", padx=8)
        self.bar_btn.pack(side=tk.RIGHT, fill=tk.Y)
        self.bar_btn.bind("<Button-1>", lambda e: self.set_compact(False))
        self.bar_btn.bind("<Enter>",
                          lambda e: self.bar_btn.config(fg=TEXT_HI, bg="#18181b"))
        self.bar_btn.bind("<Leave>",
                          lambda e: self.bar_btn.config(fg=DIM, bg="#000000"))

        for w in (self.bar_canvas, self.bar_rate, self.bar_model):
            w.bind("<Button-1>", self.drag_start)
            w.bind("<B1-Motion>", self.drag_move)
            w.bind("<ButtonRelease-1>", self.drag_end)
            w.bind("<Double-Button-1>", lambda e: self.set_compact(False))
            w.bind("<Button-3>", self.bar_menu)

    def bar_menu(self, ev):
        m = tk.Menu(self, tearoff=0, bg=PANEL, fg="#e4e4e7",
                    activebackground=EDGE, bd=0)
        m.add_command(label="Expand", command=lambda: self.set_compact(False))
        m.add_separator()
        for d in DOCKS:
            m.add_command(label=f"Dock: {d}",
                          command=lambda x=d: self.set_dock(x))
        m.add_separator()
        m.add_command(label="Toggle TRIP / TOTAL", command=self.toggle_mode)
        m.add_command(label="Reset trip", command=self.reset_trip)
        m.add_separator()
        m.add_command(label="Quit", command=self.on_close)
        m.tk_popup(ev.x_root, ev.y_root)

    # ---------------- expanded window ----------------

    def build_expanded(self):
        self.full = tk.Frame(self, bg=BG)

        head = tk.Frame(self.full, bg=BG)
        head.pack(fill=tk.X, padx=18, pady=(12, 4))

        self.mode_lbl = tk.Label(head, text=self.view_mode, fg=GREEN, bg=BG,
                                 font=("Consolas", 11, "bold"))
        self.mode_lbl.pack(side=tk.LEFT)
        self.status_lbl = tk.Label(head, text="starting...", fg=DIM, bg=BG,
                                   font=("Consolas", 9))
        self.status_lbl.pack(side=tk.LEFT, padx=12)

        collapse = tk.Label(head, text="\u25bc  collapse", fg=DIM, bg=BG,
                            cursor="hand2", font=("Segoe UI", 9))
        collapse.pack(side=tk.RIGHT)
        collapse.bind("<Button-1>", lambda e: self.set_compact(True))
        collapse.bind("<Enter>", lambda e: collapse.config(fg=TEXT_HI))
        collapse.bind("<Leave>", lambda e: collapse.config(fg=DIM))

        self.canvas = tk.Canvas(self.full, width=584, height=92, bg="#000000",
                                highlightthickness=1, highlightbackground=EDGE)
        self.canvas.pack(padx=18, pady=6)

        # ---- session budget row
        bud = tk.Frame(self.full, bg=PANEL, highlightthickness=1,
                       highlightbackground=EDGE)
        bud.pack(fill=tk.X, padx=18, pady=(4, 6))

        self.budget_on = tk.BooleanVar(value=self.budget.enabled)
        chk = tk.Checkbutton(bud, text="SESSION LIMIT", variable=self.budget_on,
                             command=self.on_budget_toggle,
                             bg=PANEL, fg=DIM, selectcolor="#000000",
                             activebackground=PANEL, activeforeground="#e4e4e7",
                             font=("Segoe UI", 8, "bold"), bd=0,
                             highlightthickness=0, cursor="hand2")
        chk.pack(side=tk.LEFT, padx=(8, 4), pady=6)

        tk.Label(bud, text="$", fg=DIM, bg=PANEL,
                 font=("Consolas", 10)).pack(side=tk.LEFT)
        self.limit_var = tk.StringVar(value=f"{self.budget.limit:g}")
        ent = tk.Entry(bud, textvariable=self.limit_var, width=7,
                       bg="#000000", fg="#e4e4e7", insertbackground="#e4e4e7",
                       relief=tk.FLAT, justify="right", font=("Consolas", 10))
        ent.pack(side=tk.LEFT, padx=(0, 6), pady=6)
        ent.bind("<Return>", lambda e: self.on_limit_change())
        ent.bind("<FocusOut>", lambda e: self.on_limit_change())

        # enforcement strictness
        self.mode_var = tk.StringVar(value=self.budget.mode)
        mode_box = ttk.Combobox(bud, textvariable=self.mode_var, width=5,
                                state="readonly", values=list(Budget.MODES))
        mode_box.pack(side=tk.LEFT, padx=(0, 8), pady=6)
        mode_box.bind("<<ComboboxSelected>>", self.on_mode_change)
        self._tooltip(mode_box,
                      "warn  - never blocks, toast only\n"
                      "soft  - blocks at 100%, one grace turn, "
                      f"hard stop at {self.budget.hard_stop_at:g}x\n"
                      "hard  - blocks at 100% immediately")

        self.budget_bar = tk.Canvas(bud, height=10, bg="#000000",
                                    highlightthickness=0)
        self.budget_bar.pack(side=tk.LEFT, fill=tk.X, expand=True,
                             padx=8, pady=6)

        self.budget_lbl = tk.Label(bud, text="off", fg=DIM, bg=PANEL,
                                   font=("Consolas", 9, "bold"), width=30,
                                   anchor="e")
        self.budget_lbl.pack(side=tk.RIGHT, padx=(4, 10))

        strip = tk.Frame(self.full, bg=BG)
        strip.pack(fill=tk.X, padx=18, pady=(2, 8))
        self.stat = {}
        for i, (k, lab, accent) in enumerate([
                ("rate", "BURN RATE", "#dc2626"),
                ("saved", "SAVED (FREE)", GREEN),
                ("tokens", "TOKENS", "#2563eb"),
                ("session", "LAST MODEL", "#7c3aed")]):
            cell = tk.Frame(strip, bg=PANEL, highlightthickness=1,
                            highlightbackground=EDGE)
            cell.grid(row=0, column=i, sticky="nsew", padx=(0 if i == 0 else 6, 0))
            strip.grid_columnconfigure(i, weight=1)
            # thin colour accent along the top edge of each card
            tk.Frame(cell, bg=accent, height=2).pack(fill=tk.X)
            tk.Label(cell, text=lab, fg=DIM, bg=PANEL,
                     font=("Segoe UI", 7, "bold")).pack(anchor="w", padx=9, pady=(6, 0))
            v = tk.Label(cell, text="-", fg=TEXT_MID, bg=PANEL,
                         font=("Consolas", 12, "bold"))
            v.pack(anchor="w", padx=9, pady=(0, 7))
            self.stat[k] = v

        tf = tk.Frame(self.full, bg=BG)
        tf.pack(fill=tk.BOTH, expand=True, padx=18, pady=(0, 6))

        st = ttk.Style()
        try:
            st.theme_use("clam")
        except tk.TclError:
            pass
        st.configure("Odo.Treeview", background=PANEL, fieldbackground=PANEL,
                     foreground=TEXT_MID, rowheight=23, borderwidth=0,
                     font=("Consolas", 9))
        st.configure("Odo.Treeview.Heading", background="#1c1c20", foreground=DIM,
                     borderwidth=0, relief=tk.FLAT, padding=(4, 5),
                     font=("Segoe UI", 8, "bold"))
        st.map("Odo.Treeview", background=[("selected", "#1e3a5f")])
        st.map("Odo.Treeview.Heading", background=[("active", "#26262b")])
        st.configure("Odo.Vertical.TScrollbar", background=EDGE, troughcolor=BG,
                     bordercolor=BG, arrowcolor=DIM, borderwidth=0)

        cols = ("model", "msgs", "tin", "tout", "cache", "cost")
        self.tree = ttk.Treeview(tf, columns=cols, show="headings",
                                 style="Odo.Treeview", height=9)
        for c, t, w, a in [("model", "MODEL", 210, "w"), ("msgs", "MSGS", 52, "e"),
                           ("tin", "IN", 78, "e"), ("tout", "OUT", 68, "e"),
                           ("cache", "CACHE", 88, "e"), ("cost", "COST $", 78, "e")]:
            self.tree.heading(c, text=t)
            self.tree.column(c, width=w, anchor=a, stretch=(c == "model"))
        self.tree.tag_configure("free", foreground=GREEN)
        self.tree.tag_configure("paid", foreground=TEXT_MID)
        self.tree.tag_configure("unknown", foreground=AMBER)
        # zebra striping, applied alongside the colour tags
        self.tree.tag_configure("odd", background="#181820")
        self.tree.tag_configure("even", background=PANEL)

        sb = ttk.Scrollbar(tf, orient="vertical", command=self.tree.yview,
                           style="Odo.Vertical.TScrollbar")
        self.tree.configure(yscrollcommand=sb.set)
        self.tree.pack(side=tk.LEFT, fill=tk.BOTH, expand=True)
        sb.pack(side=tk.RIGHT, fill=tk.Y)

        ctl = tk.Frame(self.full, bg=BG)
        ctl.pack(fill=tk.X, padx=18, pady=(0, 14))

        def btn(text, cmd, bg, side=tk.LEFT):
            hover = lighten(bg, 0.22)
            b = tk.Button(ctl, text=text, command=cmd, bg=bg, fg="white",
                          relief=tk.FLAT, padx=12, pady=5, bd=0,
                          activebackground=hover, activeforeground="white",
                          font=("Segoe UI", 8, "bold"), cursor="hand2")
            b.bind("<Enter>", lambda e, w=b, c=hover: w.config(bg=c))
            b.bind("<Leave>", lambda e, w=b, c=bg: w.config(bg=c))
            b.pack(side=side, padx=(0, 6))
            return b

        btn("TRIP / TOTAL", self.toggle_mode, EDGE)
        btn("RESET TRIP", self.reset_trip, "#7f1d1d")
        btn("RELOAD PRICES", self.reload_prices, "#1e3a8a")
        btn("DOCK", self.cycle_dock, "#3f3f46")
        btn("EXPORT CSV", self.export_csv, "#166534", side=tk.RIGHT)

    def _tooltip(self, widget, text):
        """Minimal hover tooltip - tkinter has none built in."""
        tip = {"win": None}

        def show(_=None):
            if tip["win"] or not text:
                return
            x = widget.winfo_rootx()
            y = widget.winfo_rooty() + widget.winfo_height() + 4
            w = tk.Toplevel(widget)
            w.wm_overrideredirect(True)
            w.wm_geometry(f"+{x}+{y}")
            w.configure(bg=EDGE)
            tk.Label(w, text=text, bg="#18181b", fg=TEXT_MID, justify="left",
                     font=("Consolas", 8), padx=8, pady=5).pack(padx=1, pady=1)
            tip["win"] = w

        def hide(_=None):
            if tip["win"]:
                tip["win"].destroy()
                tip["win"] = None

        widget.bind("<Enter>", show)
        widget.bind("<Leave>", hide)
        widget.bind("<ButtonPress>", hide)

    # ---------------- budget ----------------

    def rebase_budget(self, clear_overrides=False):
        """Start the counting window now: snapshot per-session spend.

        Everything already spent is grandfathered, so enabling a limit (or
        changing it) never retroactively blocks a session. Grace turns reset
        too - a new ceiling deserves a fresh allowance.
        """
        baselines = {}
        for sid, s in self.state_data.get("per_session", {}).items():
            baselines[sid] = s.get("cost", 0.0)
        self.state_data["budget_baselines"] = baselines
        self.state_data["grace_used"] = {}
        if clear_overrides:
            self.state_data["budget_allow"] = {}

    def on_budget_toggle(self):
        enabling = bool(self.budget_on.get())
        self.budget.cfg["enabled"] = enabling
        if enabling:
            self.rebase_budget()
        else:
            self.state_data["budget_baselines"] = {}
            self.state_data["grace_used"] = {}
        self.publish_budget()
        self.refresh_budget()

    def on_mode_change(self, *_):
        m = (self.mode_var.get() or "soft").strip().lower()
        if m in Budget.MODES:
            self.budget.cfg["mode"] = m
            # a stricter/looser policy starts clean
            self.state_data["grace_used"] = {}
            self.publish_budget()
            self.refresh_budget()

    def on_limit_change(self):
        try:
            v = float(self.limit_var.get())
            if v <= 0:
                raise ValueError
        except ValueError:
            self.limit_var.set(f"{self.budget.limit:g}")
            return
        if v == self.budget.limit:
            return                      # no-op; FocusOut fires constantly
        self.budget.cfg["session_limit_usd"] = v
        # new ceiling clears overrides and restarts the counting window
        self.rebase_budget(clear_overrides=True)
        self.publish_budget()
        self.refresh_budget()

    def refresh_budget(self):
        cost, sid = self.active_session_cost()
        state, frac = self.budget.status(cost)
        if sid and self.state_data.get("budget_allow", {}).get(sid, 0) >= cost:
            state = "ok"

        prev = getattr(self, "_budget_state", "ok")
        self._budget_state = state if self.budget.enabled else "ok"

        # Advice panel fires on the crossing, in either view - but only when
        # something is actually blocked. In `warn` mode nothing is refused, so
        # interrupting with a modal would be noise.
        if (self.budget.enabled and self.budget.blocks()
                and state == "over" and prev != "over"
                and not getattr(self, "_advice_open", False)):
            self.show_advice(cost, sid)

        if not hasattr(self, "budget_bar") or self.compact:
            return

        c = self.budget_bar
        c.delete("all")
        w = max(c.winfo_width(), 1)
        h = 10
        r = h / 2

        # track
        rounded_rect(c, 1, 1, w - 1, h - 1, r, fill="#000000", outline=EDGE)

        if not self.budget.enabled:
            # Still show what WOULD be counted - a limit you are about to
            # switch on should not read "off / 0%" when you are already over.
            if self.budget.limit > 0 and cost > 0:
                self.budget_lbl.config(
                    text=f"off  (${cost:.3f} / ${self.budget.limit:g})", fg=DIM)
            else:
                self.budget_lbl.config(text="off", fg=DIM)
            return

        col = {"ok": GREEN, "warn": AMBER, "over": RED}[state]
        dim = {"ok": GREEN_DIM, "warn": AMBER_DIM, "over": RED_DIM}[state]

        fill_w = int((w - 2) * min(frac, 1.0))
        if fill_w > h:
            # soft under-glow then the bright fill on top
            rounded_rect(c, 1, 0, fill_w, h, r, fill=dim, outline="")
            rounded_rect(c, 1, 1, fill_w, h - 1, r, fill=col, outline="")
        elif fill_w > 0:
            c.create_oval(1, 1, 1 + h - 2, h - 1, fill=col, outline="")

        # 80% warn marker
        wp = float(self.budget.cfg["warn_at_percent"]) / 100.0
        mx = int((w - 2) * wp)
        if 0 < mx < w - 2:
            c.create_line(mx, 1, mx, h - 1, fill="#52525b")

        if frac > 1.0:
            c.create_text(w - 5, h / 2, text="!", fill="#ffffff", anchor="e",
                          font=("Consolas", 7, "bold"))

        # Suffix tells you what will actually happen next, not just the number.
        suffix = ""
        if state == "over":
            if not self.budget.blocks():
                suffix = "  (warn only)"
            elif frac >= self.budget.hard_stop_at:
                suffix = "  HARD STOP"
            else:
                left = max(0, self.budget.grace_turns
                           - int(self.state_data.get("grace_used", {})
                                 .get(sid, 0))) if sid else 0
                suffix = f"  +{left} grace" if left else "  BLOCKED"

        self.budget_lbl.config(
            text=f"${cost:.3f} / ${self.budget.limit:g}  {frac*100:.0f}%{suffix}",
            fg=col)

    def _copy_model(self, name, widget=None):
        """Put a model id on the clipboard and flash confirmation."""
        try:
            self.clipboard_clear()
            self.clipboard_append(name)
            self.update()             # make it survive window close
        except tk.TclError:
            return
        if widget is not None:
            old = widget.cget("text")
            widget.config(text=f"  copied: {name}", fg=TEXT_HI)
            widget.after(1100, lambda: widget.config(text=old, fg=GREEN))

    def show_advice(self, cost, sid):
        """Session blew its limit. Explain, then offer real choices."""
        self._advice_open = True
        win = tk.Toplevel(self)
        win.title("Session budget exceeded")
        win.configure(bg=BG)
        win.attributes("-topmost", True)
        win.geometry("560x430")

        def closed():
            self._advice_open = False
            win.destroy()
        win.protocol("WM_DELETE_WINDOW", closed)

        # red accent stripe across the top of the dialog
        tk.Frame(win, bg=RED, height=3).pack(fill=tk.X)

        tk.Label(win, text="SESSION LIMIT REACHED", fg=RED, bg=BG,
                 font=("Segoe UI", 12, "bold")).pack(anchor="w", padx=18, pady=(16, 2))
        tk.Label(win,
                 text=f"This session has spent ${cost:.4f} of its ${self.budget.limit:g} limit.\n"
                      f"New turns are blocked until you choose below.",
                 fg=TEXT_MID, bg=BG, justify="left",
                 font=("Segoe UI", 9)).pack(anchor="w", padx=18)

        # what drove the cost
        drivers = {}
        for rec in self.messages.values():
            if rec.get("session_id") != sid:
                continue
            k = f"{rec['provider']}/{rec['model']}"
            drivers[k] = drivers.get(k, 0.0) + rec["cost"]
        top = sorted(drivers.items(), key=lambda kv: -kv[1])[:3]

        box = tk.Frame(win, bg=PANEL, highlightthickness=1, highlightbackground=EDGE)
        box.pack(fill=tk.X, padx=18, pady=10)
        tk.Label(box, text="WHAT SPENT IT", fg=DIM, bg=PANEL,
                 font=("Segoe UI", 7, "bold")).pack(anchor="w", padx=10, pady=(6, 2))
        for k, v in top:
            tk.Label(box, text=f"{k:42} ${v:.4f}", fg="#e4e4e7", bg=PANEL,
                     font=("Consolas", 9)).pack(anchor="w", padx=10)
        tk.Label(box, text="", bg=PANEL).pack(pady=2)

        # cheaper alternatives
        cur = top[0][0] if top else None
        alt = self.cheaper_than(cur)
        box2 = tk.Frame(win, bg=PANEL, highlightthickness=1, highlightbackground=EDGE)
        box2.pack(fill=tk.X, padx=18, pady=(0, 10))
        tk.Label(box2, text="CHEAPER MODELS  (click to copy \u2192 Ctrl+P in OpenCode)",
                 fg=DIM, bg=PANEL,
                 font=("Segoe UI", 7, "bold")).pack(anchor="w", padx=10, pady=(6, 2))
        if alt:
            for name, i, o, ratio in alt[:5]:
                row = tk.Label(
                    box2,
                    text=f"{name:40} ${i:>6.2f}/${o:<6.2f}  {ratio:.0%} of current",
                    fg=GREEN, bg=PANEL, cursor="hand2",
                    font=("Consolas", 9))
                row.pack(anchor="w", padx=10, fill=tk.X)
                # Clicking copies the model id: the plugin API cannot switch
                # models for you, so make the manual switch one paste away.
                row.bind("<Button-1>",
                         lambda e, n=name, w=row: self._copy_model(n, w))
                row.bind("<Enter>", lambda e, w=row: w.config(bg="#1c2b1f"))
                row.bind("<Leave>", lambda e, w=row: w.config(bg=PANEL))
        else:
            tk.Label(box2, text="  (already on a cheap model)", fg=DIM, bg=PANEL,
                     font=("Consolas", 9)).pack(anchor="w", padx=10)
        tk.Label(box2, text="", bg=PANEL).pack(pady=2)

        btns = tk.Frame(win, bg=BG)
        btns.pack(fill=tk.X, padx=18, pady=(4, 14))

        def act(text, cmd, bg):
            hover = lighten(bg, 0.22)
            b = tk.Button(btns, text=text, command=cmd, bg=bg, fg="white",
                          relief=tk.FLAT, bd=0, padx=13, pady=6, cursor="hand2",
                          activebackground=hover, activeforeground="white",
                          font=("Segoe UI", 8, "bold"))
            b.bind("<Enter>", lambda e, w=b, c=hover: w.config(bg=c))
            b.bind("<Leave>", lambda e, w=b, c=bg: w.config(bg=c))
            b.pack(side=tk.LEFT, padx=(0, 6))

        def resume():
            # allow this session to continue past its current spend
            self.state_data.setdefault("budget_allow", {})[sid] = cost + self.budget.limit
            self.state_data.setdefault("grace_used", {}).pop(sid, None)
            self.publish_budget()
            closed()
            self.refresh_budget()

        def raise_limit():
            self.budget.cfg["session_limit_usd"] = round(self.budget.limit * 2, 4)
            if hasattr(self, "limit_var"):
                self.limit_var.set(f"{self.budget.limit:g}")
            self.rebase_budget(clear_overrides=True)
            self.publish_budget()
            closed()
            self.refresh_budget()

        def disable():
            if hasattr(self, "budget_on"):
                self.budget_on.set(False)
            self.budget.cfg["enabled"] = False
            self.publish_budget()
            closed()
            self.refresh_budget()

        act(f"Allow +${self.budget.limit:g} more", resume, "#166534")
        act("Double the limit", raise_limit, "#1e3a8a")
        act("Turn enforcement off", disable, "#7f1d1d")

    # Models too weak to recommend as a coding substitute, regardless of price.
    # Suggesting an embedding model or a nano tier as an Opus replacement is
    # worse than suggesting nothing.
    _NOT_SUBSTITUTES = ("embed", "image", "-vl-", "nano", "gemma", "e5-",
                        "titan", "ocr", "asr", "realtime", "translate")

    def cheaper_than(self, key, floor_ratio=0.05):
        """Credible cheaper substitutes for `key`, ranked cheapest first.

        Only models within a sane quality band are offered: anything under
        `floor_ratio` of the current price is almost certainly a different
        class of model, not a substitute.
        """
        if not key:
            return []
        cur = self.prices.models.get(key)
        if not cur:
            return []
        base = float(cur.get("output") or 0)
        if base <= 0:
            return []

        # prefer alternatives from the same provider - they are actually
        # selectable in this OpenCode setup
        provider = key.split("/", 1)[0]

        out = []
        for k, m in self.prices.models.items():
            if k == key or m.get("free"):
                continue
            low = k.lower()
            if any(bad in low for bad in self._NOT_SUBSTITUTES):
                continue
            o = float(m.get("output") or 0)
            i = float(m.get("input") or 0)
            if o <= 0 or o >= base:
                continue
            if o / base < floor_ratio:
                continue
            out.append((k, i, o, o / base))

        same = [t for t in out if t[0].startswith(provider + "/")]
        chosen = same or out
        chosen.sort(key=lambda t: -t[3])   # closest in capability first
        return chosen

    # ---------------- shape / placement ----------------

    def set_compact(self, on):
        if self.compact == on:
            return
        self.compact = on
        self.apply_shape()
        self.save_state()

    def apply_shape(self):
        # Size constraints MUST be relaxed before resizing, otherwise the
        # expanded minsize clamps the compact bar into a giant empty window.
        self.minsize(1, 1)
        self.maxsize(self.winfo_screenwidth(), self.winfo_screenheight())

        if self.compact:
            self.full.pack_forget()
            self.bar.pack(fill=tk.BOTH, expand=True)
            self._set_frameless(True)
            self.attributes("-topmost", True)
            self.resizable(False, False)
            self.place_window(BAR_W, BAR_H)
            self.update_idletasks()
            self.draw_bar()
        else:
            self.bar.pack_forget()
            self.full.pack(fill=tk.BOTH, expand=True)
            self._set_frameless(False)
            self.attributes("-topmost", False)
            self.resizable(True, True)
            self.place_window(FULL_W, FULL_H, expanded=True)
            self.update_idletasks()
            self.minsize(FULL_W, 520)
            self.refresh_table()
            self.draw_odometer()
        self.refresh_stats()

    def _set_frameless(self, on):
        """Toggle the title bar. Windows needs a remap for this to stick."""
        if bool(self.overrideredirect()) == bool(on):
            return
        self.withdraw()
        self.overrideredirect(on)
        self.deiconify()
        self.lift()

    def place_window(self, w, h, expanded=False):
        sw = self.winfo_screenwidth()
        sh = self.winfo_screenheight()
        pos = self.state_data.get("pos")

        if expanded:
            x, y = (sw - w) // 2, max(30, (sh - h) // 2 - 40)
        elif pos:
            x, y = pos
        else:
            m = 14
            d = self.dock
            if d == "top-right":
                x, y = sw - w - m, m
            elif d == "top-center":
                x, y = (sw - w) // 2, m
            elif d == "bottom-right":
                x, y = sw - w - m, sh - h - TASKBAR
            else:  # bottom-center
                x, y = (sw - w) // 2, sh - h - TASKBAR

        x = max(0, min(int(x), sw - w))
        y = max(0, min(int(y), sh - h))
        self.geometry(f"{w}x{h}+{x}+{y}")

    def set_dock(self, d):
        self.dock = d
        self.state_data["pos"] = None
        if self.compact:
            self.place_window(BAR_W, BAR_H)
        self.save_state()

    def cycle_dock(self):
        nxt = DOCKS[(DOCKS.index(self.dock) + 1) % len(DOCKS)]
        if not self.compact:
            self.set_compact(True)
        self.set_dock(nxt)

    def drag_start(self, e):
        self._drag = (e.x_root - self.winfo_x(), e.y_root - self.winfo_y())

    def drag_move(self, e):
        if not self._drag or not self.compact:
            return
        dx, dy = self._drag
        self.geometry(f"+{e.x_root - dx}+{e.y_root - dy}")

    def drag_end(self, e):
        if self._drag and self.compact:
            self.state_data["pos"] = [self.winfo_x(), self.winfo_y()]
            self.save_state()
        self._drag = None

    # ---------------- drawing ----------------

    def current_cost(self):
        return (self.state_data["trip_cost"] if self.view_mode == "TRIP"
                else self.state_data["total_cost"])

    def draw_bar(self):
        c = self.bar_canvas
        c.delete("all")
        cost = min(self.current_cost(), 9999.9999)
        whole, frac = f"{cost:.4f}".split(".")
        whole = whole.zfill(4)

        x, y, w, h = 2, 3, 19, 30
        # budget state wins over trip/total colouring - it is the urgent signal
        bstate = getattr(self, "_budget_state", "ok")
        if self.budget.enabled and bstate != "ok":
            sigil = AMBER if bstate == "warn" else RED
        else:
            sigil = GREEN if self.view_mode == "TRIP" else RED

        # accent stripe on the far left, echoing the current state colour
        c.create_rectangle(0, y + 2, 2, y + h - 2, fill=sigil, outline="")

        c.create_text(x + 6, y + h / 2, text="$", fill=sigil,
                      font=("Consolas", 13, "bold"))
        x += 14
        for d in whole:
            beveled_cell(c, x, y, w, h, CELL_WHOLE_BG,
                         CELL_WHOLE_BORDER, CELL_WHOLE_HILITE)
            c.create_text(x + w / 2, y + h / 2, text=d, fill=TEXT_HI,
                          font=("Consolas", 15, "bold"))
            x += w + 2

        # decimal point
        c.create_oval(x + 2, y + h - 8, x + 7, y + h - 3, fill=sigil, outline="")
        x += 11

        for d in frac:
            beveled_cell(c, x, y, w, h, CELL_FRAC_BG,
                         CELL_FRAC_BORDER, CELL_FRAC_HILITE)
            c.create_text(x + w / 2, y + h / 2, text=d, fill="#09090b",
                          font=("Consolas", 15, "bold"))
            x += w + 2

        # activity indicator: soft pulsing ring rather than a hard dot
        if self.active():
            self._pulse = (self._pulse + 1) % 2
            cx, cy = x + 9, y + h / 2
            if self._pulse:
                c.create_oval(cx - 6, cy - 6, cx + 6, cy + 6,
                              outline=GREEN_DIM, width=1)
            c.create_oval(cx - 3, cy - 3, cx + 3, cy + 3,
                          fill=GREEN if self._pulse else GREEN_GLOW, outline="")

    def draw_odometer(self):
        c = self.canvas
        c.delete("all")
        cost = min(self.current_cost(), 99999.99999)
        whole, frac = f"{cost:.5f}".split(".")
        whole = whole.zfill(5)

        bstate = getattr(self, "_budget_state", "ok")
        if self.budget.enabled and bstate != "ok":
            accent = AMBER if bstate == "warn" else RED
        else:
            accent = GREEN if self.view_mode == "TRIP" else RED

        # left accent stripe ties the odometer to the current state
        c.create_rectangle(0, 0, 3, 92, fill=accent, outline="")

        x, y, w, h = 22, 14, 42, 64
        c.create_text(x + 8, y + h / 2, text="$", fill=accent,
                      font=("Consolas", 26, "bold"))
        x += 26
        for d in whole:
            beveled_cell(c, x, y, w, h, CELL_WHOLE_BG,
                         CELL_WHOLE_BORDER, CELL_WHOLE_HILITE)
            c.create_text(x + w / 2, y + h / 2, text=d, fill=TEXT_HI,
                          font=("Consolas", 30, "bold"))
            x += w + 3

        c.create_oval(x + 4, y + h - 13, x + 13, y + h - 4,
                      fill=accent, outline="")
        x += 19

        for d in frac:
            beveled_cell(c, x, y, w, h, CELL_FRAC_BG,
                         CELL_FRAC_BORDER, CELL_FRAC_HILITE)
            c.create_text(x + w / 2, y + h / 2, text=d, fill="#09090b",
                          font=("Consolas", 30, "bold"))
            x += w + 3

        # mode badge, bottom-left under the digits
        c.create_text(24, 86, text=self.view_mode, fill=DIM, anchor="w",
                      font=("Segoe UI", 7, "bold"))

        if self.active():
            self._pulse = (self._pulse + 1) % 2
            cx, cy = 562, 26
            if self._pulse:
                c.create_oval(cx - 9, cy - 9, cx + 9, cy + 9,
                              outline=GREEN_DIM, width=1)
            c.create_oval(cx - 5, cy - 5, cx + 5, cy + 5,
                          fill=GREEN if self._pulse else GREEN_GLOW, outline="")
            c.create_text(cx, cy + 18, text="LIVE", fill=GREEN_GLOW,
                          font=("Segoe UI", 6, "bold"))

    def active(self):
        return (self._last_activity and
                (datetime.now() - self._last_activity).total_seconds() < 6)

    # ---------------- polling ----------------

    def tick(self):
        try:
            self.poll_once()
        except Exception as e:
            if not self.compact:
                self.status_lbl.config(text=f"error: {e}", fg=RED)
        if self.compact:
            self.draw_bar()
        else:
            self.draw_odometer()
        self.after(POLL_MS, self.tick)

    def seed_history(self):
        if not self.compact:
            self.status_lbl.config(text="importing history...", fg=AMBER)
        self.update_idletasks()
        self.poll_once(seeding=True)
        self.state_data["seeded"] = True
        # existing history is context, not this trip
        self.state_data["trip_base"] = self.state_data["total_cost"]
        self.state_data["trip_saved_base"] = self.state_data["total_saved"]
        self.state_data["trip_cost"] = 0.0
        self.state_data["trip_saved"] = 0.0
        self.save_state()
        self.refresh_table()

    def poll_once(self, seeding=False):
        # Grace claims arrive independently of DB activity, so absorb them
        # before any early return or a used grace turn would never register.
        if not seeding and self.absorb_grace_claims():
            self.publish_budget()
            self.save_state()

        if not self.db.available():
            if not self.compact:
                self.status_lbl.config(text="opencode.db not found", fg=RED)
            return

        # cheap guard: only snapshot when the DB actually changed
        mt = self.db.mtime()
        if not seeding and mt == self._db_mtime:
            self.refresh_stats()
            return
        self._db_mtime = mt

        # Re-read from one message before the watermark, because rows are
        # updated in place after insert. Records are keyed by message id and
        # replaced, so re-reading is idempotent - never double counts.
        rows = self.db.fetch_since(self.state_data.get("last_updated", 0))
        if not rows:
            self.refresh_stats()
            return

        changed = 0
        newest = self.state_data.get("last_updated", 0)

        for mid, sid, tc, tu, rec in rows:
            newest = max(newest, tu)
            key = f"{rec['provider']}/{rec['model']}"
            e = self.prices.entry(key)
            free = bool(e.get("free"))
            cost = self.prices.cost_of(key, rec["in"], rec["out"],
                                       rec["cache_read"], rec["cache_write"])
            saved = (self.prices.shadow_cost(key, rec["in"], rec["out"],
                                             rec["cache_read"], rec["cache_write"])
                     if free else 0.0)

            prev = self.messages.get(mid)
            if prev and (prev["in"], prev["out"], prev["cache_read"],
                         prev["cache_write"]) == (rec["in"], rec["out"],
                                                  rec["cache_read"],
                                                  rec["cache_write"]):
                continue  # nothing new for this message

            gained = cost - (prev["cost"] if prev else 0.0)

            self.messages[mid] = {
                "timestamp": datetime.fromtimestamp(tc / 1000).strftime("%Y-%m-%d %H:%M:%S"),
                "session_id": sid,
                "provider": rec["provider"], "model": rec["model"], "free": free,
                "in": rec["in"], "out": rec["out"],
                "reasoning": rec["reasoning"],
                "cache_read": rec["cache_read"], "cache_write": rec["cache_write"],
                "cost": cost, "saved": saved,
            }

            if not seeding and gained > 0:
                self._rate_window.append((datetime.now().timestamp(), gained))
            if rec["in"] or rec["out"]:
                self._last_model = key
            changed += 1

        self.state_data["last_updated"] = newest

        if changed:
            self.recompute()
            self._last_activity = datetime.now()
            self.save_state()
            if not self.compact:
                self.refresh_table()
                self.status_lbl.config(text=f"{changed} msg updated", fg=GREEN)
        self.refresh_stats()

    def recompute(self):
        """Rebuild all totals from the per-message store (single source of truth)."""
        total = saved_total = 0.0
        per = {}
        per_session = {}
        for rec in self.messages.values():
            sid = rec.get("session_id")
            if sid:
                s = per_session.setdefault(sid, {"cost": 0.0, "msgs": 0,
                                                 "tokens": 0, "last": "",
                                                 "model": ""})
                s["cost"] += rec["cost"]
                s["msgs"] += 1
                s["tokens"] += (rec["in"] + rec["out"]
                                + rec["cache_read"] + rec["cache_write"])
                if rec["timestamp"] > s["last"]:
                    s["last"] = rec["timestamp"]
                    s["model"] = f"{rec['provider']}/{rec['model']}"
        for rec in self.messages.values():
            key = f"{rec['provider']}/{rec['model']}"
            e = self.prices.entry(key)
            total += rec["cost"]
            saved_total += rec["saved"]
            pm = per.setdefault(key, {"msgs": 0, "in": 0, "out": 0, "cr": 0,
                                      "cw": 0, "cost": 0.0, "saved": 0.0,
                                      "free": rec["free"],
                                      "unknown": bool(e.get("unknown"))})
            pm["msgs"] += 1
            pm["in"] += rec["in"]
            pm["out"] += rec["out"]
            pm["cr"] += rec["cache_read"]
            pm["cw"] += rec["cache_write"]
            pm["cost"] += rec["cost"]
            pm["saved"] += rec["saved"]

        # spend from messages that have since been pruned still counts
        total += self.state_data.get("pruned_cost", 0.0)
        saved_total += self.state_data.get("pruned_saved", 0.0)

        self.state_data["per_model"] = per
        self.state_data["per_session"] = per_session
        self.state_data["total_cost"] = total
        self.state_data["total_saved"] = saved_total
        # trip = everything since the last reset baseline
        self.state_data["trip_cost"] = max(0.0, total - self.state_data.get("trip_base", 0.0))
        self.state_data["trip_saved"] = max(0.0, saved_total - self.state_data.get("trip_saved_base", 0.0))
        self.publish_budget()

    def absorb_grace_claims(self):
        """Fold plugin-written grace claims into state, then clear the file.

        The plugin cannot write budget.json (single-writer invariant), so it
        records "I used a grace turn for session X" in a small side file.
        """
        if not os.path.exists(GRACE_FILE):
            return False
        try:
            with open(GRACE_FILE, "r", encoding="utf-8") as f:
                claims = json.load(f) or {}
        except Exception:
            claims = {}
        if not claims:
            try:
                os.remove(GRACE_FILE)
            except OSError:
                pass
            return False

        used = self.state_data.setdefault("grace_used", {})
        for sid, n in claims.items():
            try:
                used[sid] = int(used.get(sid, 0)) + int(n)
            except (TypeError, ValueError):
                continue
        try:
            os.remove(GRACE_FILE)
        except OSError:
            pass
        print(f"[budget] absorbed grace claims for {len(claims)} session(s)")
        return True

    def publish_budget(self):
        """Write the verdict the plugin enforces on.

        Only recently active sessions are published; the plugin looks up by
        session id, and an unbounded ledger would grow forever.
        """
        allow = self.state_data.get("budget_allow", {})
        used = self.state_data.get("grace_used", {})
        hard_x = self.budget.hard_stop_at
        out = {}
        for sid, s in self.state_data.get("per_session", {}).items():
            eff = self.effective_session_cost(sid, s["cost"])
            state, frac = self.budget.status(eff)
            if allow.get(sid, 0) >= eff:
                state = "ok"          # user granted an override past this point

            # Grace is offered only in `soft` mode, once per session, and only
            # below the hard ceiling. Past the ceiling a runaway loop must stop
            # regardless of how many grace turns are nominally left.
            remaining = 0
            if state == "over" and self.budget.mode == "soft":
                if frac < hard_x:
                    remaining = max(0, self.budget.grace_turns
                                    - int(used.get(sid, 0)))

            out[sid] = {
                "cost": round(eff, 6),
                "state": state,
                "fraction": round(frac, 4),
                "limit": self.budget.limit,
                "mode": self.budget.mode,
                "grace_remaining": remaining,
                "hard_stop_at": hard_x,
                "past_hard_stop": bool(frac >= hard_x),
                # `state` is now a pure measurement, so say explicitly whether
                # it should be acted on. The plugin must never infer this.
                "enforced": self.budget.blocks(),
            }
        # keep the file small: most recent 60 sessions by last activity
        recent = sorted(self.state_data.get("per_session", {}).items(),
                        key=lambda kv: kv[1].get("last", ""), reverse=True)[:60]
        self.budget.save({sid: out[sid] for sid, _ in recent if sid in out})

    def effective_session_cost(self, sid, raw_cost):
        """Return session cost minus the baseline recorded when the limit was enabled.

        This ensures that spend accrued *before* the user turned on the
        session limit is grandfathered and does not count towards the cap.
        """
        baseline = self.state_data.get("budget_baselines", {}).get(sid, 0.0)
        return max(0.0, raw_cost - baseline)

    def active_session(self):
        """Session id of the most recent assistant message."""
        best, when = None, ""
        for rec in self.messages.values():
            if rec["timestamp"] > when:
                when, best = rec["timestamp"], rec.get("session_id")
        return best

    def active_session_cost(self):
        sid = self.active_session()
        if not sid:
            return 0.0, None
        raw = self.state_data.get("per_session", {}).get(
            sid, {"cost": 0.0})["cost"]
        return self.effective_session_cost(sid, raw), sid

    # ---------------- derived views ----------------

    def refresh_stats(self):
        now = datetime.now().timestamp()
        self._rate_window = [(t, c) for t, c in self._rate_window if now - t <= 600]
        rate = sum(c for _, c in self._rate_window) * 6  # 10min -> per hour

        saved = (self.state_data["trip_saved"] if self.view_mode == "TRIP"
                 else self.state_data["total_saved"])
        tot = sum(v["in"] + v["out"] + v["cr"] + v["cw"]
                  for v in self.state_data["per_model"].values())
        last = self._last_model
        short = last.split("/", 1)[-1][:20] if last else "idle"
        lfree = self.prices.is_free(last) if last else False

        if self.compact:
            self.bar_rate.config(text=f"${rate:,.2f}/hr",
                                 fg=RED if rate > 5 else (DIM if not rate else "#e4e4e7"))
            self.bar_model.config(text=short, fg=GREEN if lfree else DIM)
        else:
            self.stat["rate"].config(text=f"${rate:,.2f}/hr",
                                     fg=RED if rate > 5 else ("#e4e4e7" if rate else DIM))
            self.stat["saved"].config(text=f"${saved:,.2f}", fg=GREEN)
            self.stat["tokens"].config(text=self.human(tot))
            self.stat["session"].config(text=short,
                                        fg=GREEN if lfree else "#e4e4e7")
            self.refresh_budget()
            idle = ""
            if self._last_activity:
                s = int((datetime.now() - self._last_activity).total_seconds())
                idle = " | ACTIVE" if s <= 3 else f" | idle {s}s"
            if "msg" not in self.status_lbl.cget("text"):
                self.status_lbl.config(text=f"watching{idle}", fg=DIM)

    @staticmethod
    def human(n):
        for u, d in (("B", 1e9), ("M", 1e6), ("K", 1e3)):
            if n >= d:
                return f"{n / d:.1f}{u}"
        return str(int(n))

    def refresh_table(self):
        if not hasattr(self, "tree"):
            return
        for i in self.tree.get_children():
            self.tree.delete(i)
        for n, (key, v) in enumerate(sorted(
                self.state_data["per_model"].items(),
                key=lambda kv: (-kv[1]["cost"],
                                -(kv[1]["in"] + kv[1]["out"])))):
            if v.get("unknown"):
                tag, label = "unknown", f"? {key}"
            elif v["free"]:
                tag, label = "free", f"\u2022 {key}"
            else:
                tag, label = "paid", f"  {key}"
            stripe = "odd" if n % 2 else "even"
            self.tree.insert("", "end", tags=(stripe, tag), values=(
                label, v["msgs"], self.human(v["in"]), self.human(v["out"]),
                self.human(v["cr"] + v["cw"]),
                "FREE" if v["free"] else f"{v['cost']:.4f}"))

    # ---------------- actions ----------------

    def toggle_mode(self):
        self.view_mode = "TOTAL" if self.view_mode == "TRIP" else "TRIP"
        if hasattr(self, "mode_lbl"):
            self.mode_lbl.config(text=self.view_mode,
                                 fg=RED if self.view_mode == "TOTAL" else GREEN)
        self.save_state()
        self.refresh_stats()

    def reset_trip(self):
        self.state_data["trip_base"] = self.state_data["total_cost"]
        self.state_data["trip_saved_base"] = self.state_data["total_saved"]
        self.state_data["trip_cost"] = 0.0
        self.state_data["trip_saved"] = 0.0
        self._rate_window.clear()
        self.view_mode = "TRIP"
        if hasattr(self, "mode_lbl"):
            self.mode_lbl.config(text="TRIP", fg=GREEN)
        self.save_state()
        self.refresh_stats()

    def reload_prices(self):
        self.prices.load()
        # re-price every stored message, then rebuild totals
        for rec in self.messages.values():
            key = f"{rec['provider']}/{rec['model']}"
            free = self.prices.is_free(key)
            rec["free"] = free
            rec["cost"] = self.prices.cost_of(key, rec["in"], rec["out"],
                                              rec["cache_read"], rec["cache_write"])
            rec["saved"] = (self.prices.shadow_cost(
                key, rec["in"], rec["out"],
                rec["cache_read"], rec["cache_write"]) if free else 0.0)
        self.recompute()
        self.save_state()
        self.refresh_table()
        self.refresh_stats()
        msg = f"Reloaded {len(self.prices.models)} models."
        if self.prices.unknown:
            msg += ("\n\nNot in prices.json (counted as $0):\n  "
                    + "\n  ".join(sorted(self.prices.unknown)[:15]))
        messagebox.showinfo("Prices", msg)

    def export_csv(self):
        if not self.messages:
            messagebox.showinfo("Export", "Nothing recorded yet.")
            return
        fn = os.path.join(APP_DIR,
                          f"opencode_cost_{datetime.now():%Y-%m-%d_%H%M}.csv")
        try:
            with open(fn, "w", newline="", encoding="utf-8") as f:
                w = csv.writer(f)
                w.writerow(["timestamp", "message_id", "session_id", "provider",
                            "model", "free", "tokens_in", "tokens_out",
                            "reasoning", "cache_read", "cache_write",
                            "cost_usd", "saved_usd"])
                for mid, h in sorted(self.messages.items(),
                                     key=lambda kv: kv[1]["timestamp"]):
                    w.writerow([h["timestamp"], mid, h["session_id"],
                                h["provider"], h["model"],
                                "yes" if h["free"] else "no",
                                h["in"], h["out"], h.get("reasoning", 0),
                                h["cache_read"], h["cache_write"],
                                f"{h['cost']:.6f}", f"{h['saved']:.6f}"])
                w.writerow([])
                w.writerow(["TOTAL", "", "", "", "", "", "", "", "", "", "",
                            f"{self.state_data['total_cost']:.6f}",
                            f"{self.state_data['total_saved']:.6f}"])
            messagebox.showinfo("Export", f"Saved:\n{fn}")
        except Exception as e:
            messagebox.showerror("Export failed", str(e))


if __name__ == "__main__":
    if not acquire_lock():
        sys.exit(0)
    try:
        OpenCodeOdometer().mainloop()
    finally:
        release_lock()
