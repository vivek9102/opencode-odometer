export namespace app {

	export class Alternative {
	    key: string;
	    name: string;
	    provider: string;
	    input: number;
	    output: number;
	    free: boolean;
	    ratio: number;
	    unknown: boolean;
	    estimated: boolean;
	    source: string;
	    category: string;
	    current: boolean;
	    cheaper: boolean;
	    comparable: boolean;

	    static createFrom(source: any = {}) {
	        return new Alternative(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.name = source["name"];
	        this.provider = source["provider"];
	        this.input = source["input"];
	        this.output = source["output"];
	        this.free = source["free"];
	        this.ratio = source["ratio"];
	        this.unknown = source["unknown"];
	        this.estimated = source["estimated"];
	        this.source = source["source"];
	        this.category = source["category"];
	        this.current = source["current"];
	        this.cheaper = source["cheaper"];
	        this.comparable = source["comparable"];
	    }
	}
	export class Charge {
	    sequence: number;
	    id: string;
	    cost: number;

	    static createFrom(source: any = {}) {
	        return new Charge(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sequence = source["sequence"];
	        this.id = source["id"];
	        this.cost = source["cost"];
	    }
	}
	export class ModelSwitch {
	    id: string;
	    session_id: string;
	    key: string;
	    status: string;
	    detail: string;
	    issued: number;
	    message_id?: string;
	    persistent?: boolean;

	    static createFrom(source: any = {}) {
	        return new ModelSwitch(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.session_id = source["session_id"];
	        this.key = source["key"];
	        this.status = source["status"];
	        this.detail = source["detail"];
	        this.issued = source["issued"];
	        this.message_id = source["message_id"];
	        this.persistent = source["persistent"];
	    }
	}
	export class OpenSessionRow {
	    id: string;
	    name: string;
	    pid: number;
	    started: number;
	    updated: number;
	    session_id: string;
	    model: string;
	    active: boolean;
	    closed: boolean;
	    cost: number;
	    spent: number;
	    enabled: boolean;
	    limit: number;
	    mode: string;
	    state: string;
	    fraction: number;
	    grace_remaining: number;
	    past_hard_stop: boolean;
	    enforced: boolean;

	    static createFrom(source: any = {}) {
	        return new OpenSessionRow(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.pid = source["pid"];
	        this.started = source["started"];
	        this.updated = source["updated"];
	        this.session_id = source["session_id"];
	        this.model = source["model"];
	        this.active = source["active"];
	        this.closed = source["closed"];
	        this.cost = source["cost"];
	        this.spent = source["spent"];
	        this.enabled = source["enabled"];
	        this.limit = source["limit"];
	        this.mode = source["mode"];
	        this.state = source["state"];
	        this.fraction = source["fraction"];
	        this.grace_remaining = source["grace_remaining"];
	        this.past_hard_stop = source["past_hard_stop"];
	        this.enforced = source["enforced"];
	    }
	}
	export class Preferences {
	    auto_start: boolean;
	    auto_collapse: boolean;
	    idle_dim: boolean;
	    remaining: boolean;
	    paused: boolean;

	    static createFrom(source: any = {}) {
	        return new Preferences(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.auto_start = source["auto_start"];
	        this.auto_collapse = source["auto_collapse"];
	        this.idle_dim = source["idle_dim"];
	        this.remaining = source["remaining"];
	        this.paused = source["paused"];
	    }
	}
	export class PricePreview {
	    key: string;
	    cost: number;
	    source: string;
	    estimated: boolean;
	    free: boolean;
	    unknown: boolean;
	    reported_cost: number;
	    input_rate: number;
	    output_rate: number;
	    cache_read_rate: number;
	    cache_write_rate: number;

	    static createFrom(source: any = {}) {
	        return new PricePreview(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.cost = source["cost"];
	        this.source = source["source"];
	        this.estimated = source["estimated"];
	        this.free = source["free"];
	        this.unknown = source["unknown"];
	        this.reported_cost = source["reported_cost"];
	        this.input_rate = source["input_rate"];
	        this.output_rate = source["output_rate"];
	        this.cache_read_rate = source["cache_read_rate"];
	        this.cache_write_rate = source["cache_write_rate"];
	    }
	}

}

export namespace prices {

	export class Rate {
	    name: string;
	    family?: string;
	    source?: string;
	    input: number;
	    output: number;
	    cache_read: number;
	    cache_write: number;
	    free?: boolean;
	    sovereign?: boolean;
	    unknown?: boolean;

	    static createFrom(source: any = {}) {
	        return new Rate(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.family = source["family"];
	        this.source = source["source"];
	        this.input = source["input"];
	        this.output = source["output"];
	        this.cache_read = source["cache_read"];
	        this.cache_write = source["cache_write"];
	        this.free = source["free"];
	        this.sovereign = source["sovereign"];
	        this.unknown = source["unknown"];
	    }
	}
	export class PriceSuggestion {
	    key: string;
	    suggested_rate: Rate;
	    source: string;
	    is_unknown: boolean;
	    is_local: boolean;
	    needs_confirmation: boolean;

	    static createFrom(source: any = {}) {
	        return new PriceSuggestion(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.suggested_rate = this.convertValues(source["suggested_rate"], Rate);
	        this.source = source["source"];
	        this.is_unknown = source["is_unknown"];
	        this.is_local = source["is_local"];
	        this.needs_confirmation = source["needs_confirmation"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace wservice {

	export class AdviceInfo {
	    session_id: string;
	    cost: number;
	    limit: number;
	    mode: string;
	    drivers: app.Alternative[];
	    cheaper: app.Alternative[];
	    current: string;
	    tokens: number;
	    msgs: number;
	    rate: number;
	    saved: number;
	    trip_cost: number;
	    total_cost: number;
	    fraction: number;
	    state: string;
	    enforced: boolean;
	    grace_remaining: number;
	    since: string;

	    static createFrom(source: any = {}) {
	        return new AdviceInfo(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session_id = source["session_id"];
	        this.cost = source["cost"];
	        this.limit = source["limit"];
	        this.mode = source["mode"];
	        this.drivers = this.convertValues(source["drivers"], app.Alternative);
	        this.cheaper = this.convertValues(source["cheaper"], app.Alternative);
	        this.current = source["current"];
	        this.tokens = source["tokens"];
	        this.msgs = source["msgs"];
	        this.rate = source["rate"];
	        this.saved = source["saved"];
	        this.trip_cost = source["trip_cost"];
	        this.total_cost = source["total_cost"];
	        this.fraction = source["fraction"];
	        this.state = source["state"];
	        this.enforced = source["enforced"];
	        this.grace_remaining = source["grace_remaining"];
	        this.since = source["since"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ModelOption {
	    key: string;
	    name: string;
	    provider: string;
	    tier: string;
	    input_cost: number;
	    output_cost: number;
	    free: boolean;
	    description: string;
	    unknown: boolean;
	    estimated: boolean;
	    category: string;
	    cheaper: boolean;
	    current: boolean;
	    source: string;

	    static createFrom(source: any = {}) {
	        return new ModelOption(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.name = source["name"];
	        this.provider = source["provider"];
	        this.tier = source["tier"];
	        this.input_cost = source["input_cost"];
	        this.output_cost = source["output_cost"];
	        this.free = source["free"];
	        this.description = source["description"];
	        this.unknown = source["unknown"];
	        this.estimated = source["estimated"];
	        this.category = source["category"];
	        this.cheaper = source["cheaper"];
	        this.current = source["current"];
	        this.source = source["source"];
	    }
	}
	export class ModelRow {
	    key: string;
	    msgs: number;
	    in: number;
	    out: number;
	    cache: number;
	    cost: number;
	    saved: number;
	    free: boolean;
	    unknown: boolean;
	    estimated: boolean;
	    source?: string;

	    static createFrom(source: any = {}) {
	        return new ModelRow(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.msgs = source["msgs"];
	        this.in = source["in"];
	        this.out = source["out"];
	        this.cache = source["cache"];
	        this.cost = source["cost"];
	        this.saved = source["saved"];
	        this.free = source["free"];
	        this.unknown = source["unknown"];
	        this.estimated = source["estimated"];
	        this.source = source["source"];
	    }
	}
	export class Snapshot {
	    open_sessions: app.OpenSessionRow[];
	    selected_session: string;
	    open_cost: number;
	    open_rate: number;
	    view: string;
	    cost: number;
	    saved: number;
	    trip_cost: number;
	    total_cost: number;
	    rate: number;
	    model: string;
	    tokens: number;
	    seeded: boolean;
	    active: boolean;
	    budget_enabled: boolean;
	    limit: number;
	    exceeded: boolean;
	    mode: string;
	    hard_stop_at: number;
	    fraction: number;
	    budget_label: string;
	    budget_state: string;
	    session_cost: number;
	    session_id: string;
	    grace_remaining: number;
	    past_hard_stop: boolean;
	    enforced: boolean;
	    session_count: number;
	    other_cost: number;
	    warn_at: number;
	    unpriced_models: number;
	    unpriced_tokens: number;
	    connected: boolean;
	    server_url: string;
	    link: string;
	    idle_for: string;
	    estimated_models: number;
	    via: string;
	    dock: string;
	    docked: boolean;
	    compact: boolean;
	    rows: ModelRow[];
	    preferences: app.Preferences;
	    charge: app.Charge;
	    charges: app.Charge[];
	    sparkline: number[];
	    peek: boolean;

	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.open_sessions = this.convertValues(source["open_sessions"], app.OpenSessionRow);
	        this.selected_session = source["selected_session"];
	        this.open_cost = source["open_cost"];
	        this.open_rate = source["open_rate"];
	        this.view = source["view"];
	        this.cost = source["cost"];
	        this.saved = source["saved"];
	        this.trip_cost = source["trip_cost"];
	        this.total_cost = source["total_cost"];
	        this.rate = source["rate"];
	        this.model = source["model"];
	        this.tokens = source["tokens"];
	        this.seeded = source["seeded"];
	        this.active = source["active"];
	        this.budget_enabled = source["budget_enabled"];
	        this.limit = source["limit"];
	        this.exceeded = source["exceeded"];
	        this.mode = source["mode"];
	        this.hard_stop_at = source["hard_stop_at"];
	        this.fraction = source["fraction"];
	        this.budget_label = source["budget_label"];
	        this.budget_state = source["budget_state"];
	        this.session_cost = source["session_cost"];
	        this.session_id = source["session_id"];
	        this.grace_remaining = source["grace_remaining"];
	        this.past_hard_stop = source["past_hard_stop"];
	        this.enforced = source["enforced"];
	        this.session_count = source["session_count"];
	        this.other_cost = source["other_cost"];
	        this.warn_at = source["warn_at"];
	        this.unpriced_models = source["unpriced_models"];
	        this.unpriced_tokens = source["unpriced_tokens"];
	        this.connected = source["connected"];
	        this.server_url = source["server_url"];
	        this.link = source["link"];
	        this.idle_for = source["idle_for"];
	        this.estimated_models = source["estimated_models"];
	        this.via = source["via"];
	        this.dock = source["dock"];
	        this.docked = source["docked"];
	        this.compact = source["compact"];
	        this.rows = this.convertValues(source["rows"], ModelRow);
	        this.preferences = this.convertValues(source["preferences"], app.Preferences);
	        this.charge = this.convertValues(source["charge"], app.Charge);
	        this.charges = this.convertValues(source["charges"], app.Charge);
	        this.sparkline = source["sparkline"];
	        this.peek = source["peek"];
	    }

		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}
