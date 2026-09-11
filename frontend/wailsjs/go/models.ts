export namespace app {
	
	export class Alternative {
	    key: string;
	    name: string;
	    provider: string;
	    input: number;
	    output: number;
	    free: boolean;
	    ratio: number;
	
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
	
	    static createFrom(source: any = {}) {
	        return new PriceSuggestion(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.suggested_rate = this.convertValues(source["suggested_rate"], Rate);
	        this.source = source["source"];
	        this.is_unknown = source["is_unknown"];
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
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
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

