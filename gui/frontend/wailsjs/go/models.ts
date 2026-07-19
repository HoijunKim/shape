export namespace query {
	
	export enum CellKind {
	    ARRAY = "array",
	    BOOL = "bool",
	    FLOAT = "float",
	    INT = "int",
	    MISSING = "missing",
	    NULL = "null",
	    OBJECT = "object",
	    STRING = "string",
	}
	export class Cell {
	    kind: CellKind;
	    str?: string;
	    num?: number;
	    bool?: boolean;
	    count?: number;
	    hasMore?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Cell(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.str = source["str"];
	        this.num = source["num"];
	        this.bool = source["bool"];
	        this.count = source["count"];
	        this.hasMore = source["hasMore"];
	    }
	}
	export class Column {
	    path: string;
	    name: string;
	    type: string;
	    nullable: boolean;
	    presence: number;
	    distinct: number;
	    container: boolean;
	    index: number;
	
	    static createFrom(source: any = {}) {
	        return new Column(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.type = source["type"];
	        this.nullable = source["nullable"];
	        this.presence = source["presence"];
	        this.distinct = source["distinct"];
	        this.container = source["container"];
	        this.index = source["index"];
	    }
	}
	export class ColumnSpec {
	    path: string;
	    as?: string;
	
	    static createFrom(source: any = {}) {
	        return new ColumnSpec(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.as = source["as"];
	    }
	}
	export class Value {
	    kind: string;
	    str?: string;
	    num?: number;
	    bool?: boolean;
	    list?: Value[];
	
	    static createFrom(source: any = {}) {
	        return new Value(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.str = source["str"];
	        this.num = source["num"];
	        this.bool = source["bool"];
	        this.list = this.convertValues(source["list"], Value);
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
	export class Condition {
	    path: string;
	    op: string;
	    value?: Value;
	    ci?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Condition(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.op = source["op"];
	        this.value = this.convertValues(source["value"], Value);
	        this.ci = source["ci"];
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
	export class Filter {
	    combinator: string;
	    conditions?: Condition[];
	    groups?: Filter[];
	    negate?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Filter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.combinator = source["combinator"];
	        this.conditions = this.convertValues(source["conditions"], Condition);
	        this.groups = this.convertValues(source["groups"], Filter);
	        this.negate = source["negate"];
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
	export class CountRequest {
	    requestId?: string;
	    handle: string;
	    filter: Filter;
	
	    static createFrom(source: any = {}) {
	        return new CountRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.requestId = source["requestId"];
	        this.handle = source["handle"];
	        this.filter = this.convertValues(source["filter"], Filter);
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
	export class CountResult {
	    total: number;
	    exact: boolean;
	    elapsedMs: number;
	
	    static createFrom(source: any = {}) {
	        return new CountResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.exact = source["exact"];
	        this.elapsedMs = source["elapsedMs"];
	    }
	}
	export class ValueCount {
	    value: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new ValueCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.value = source["value"];
	        this.count = source["count"];
	    }
	}
	export class TypeShare {
	    kind: string;
	    share: number;
	
	    static createFrom(source: any = {}) {
	        return new TypeShare(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.share = source["share"];
	    }
	}
	export class FieldDTO {
	    path: string;
	    types: TypeShare[];
	    presence: number;
	    nullRate: number;
	    distinct: number;
	    distinctExact: boolean;
	    min?: number;
	    max?: number;
	    topValues?: ValueCount[];
	    drift: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FieldDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.types = this.convertValues(source["types"], TypeShare);
	        this.presence = source["presence"];
	        this.nullRate = source["nullRate"];
	        this.distinct = source["distinct"];
	        this.distinctExact = source["distinctExact"];
	        this.min = source["min"];
	        this.max = source["max"];
	        this.topValues = this.convertValues(source["topValues"], ValueCount);
	        this.drift = source["drift"];
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
	
	export class OpenRequest {
	    requestId?: string;
	    path: string;
	    format?: string;
	    table?: string;
	    csvRaw?: boolean;
	    budgetMB?: number;
	
	    static createFrom(source: any = {}) {
	        return new OpenRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.requestId = source["requestId"];
	        this.path = source["path"];
	        this.format = source["format"];
	        this.table = source["table"];
	        this.csvRaw = source["csvRaw"];
	        this.budgetMB = source["budgetMB"];
	    }
	}
	export class ProfileDTO {
	    records: number;
	    skipped: number;
	    fields: FieldDTO[];
	
	    static createFrom(source: any = {}) {
	        return new ProfileDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.records = source["records"];
	        this.skipped = source["skipped"];
	        this.fields = this.convertValues(source["fields"], FieldDTO);
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
	export class OpenResult {
	    handle: string;
	    format: string;
	    tier: string;
	    columns: Column[];
	    profile: ProfileDTO;
	    sampled: boolean;
	    rowEstimate: number;
	    rowExact: boolean;
	    warnings?: string[];
	    columnsTruncated: boolean;
	    totalPaths: number;
	
	    static createFrom(source: any = {}) {
	        return new OpenResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.handle = source["handle"];
	        this.format = source["format"];
	        this.tier = source["tier"];
	        this.columns = this.convertValues(source["columns"], Column);
	        this.profile = this.convertValues(source["profile"], ProfileDTO);
	        this.sampled = source["sampled"];
	        this.rowEstimate = source["rowEstimate"];
	        this.rowExact = source["rowExact"];
	        this.warnings = source["warnings"];
	        this.columnsTruncated = source["columnsTruncated"];
	        this.totalPaths = source["totalPaths"];
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
	
	export class Transform {
	    select?: ColumnSpec[];
	    drop?: string[];
	    flattenObjects: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Transform(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.select = this.convertValues(source["select"], ColumnSpec);
	        this.drop = source["drop"];
	        this.flattenObjects = source["flattenObjects"];
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
	export class QueryRequest {
	    requestId?: string;
	    handle: string;
	    filter: Filter;
	    transform: Transform;
	    offset: number;
	    limit: number;
	    wantTotal: boolean;
	
	    static createFrom(source: any = {}) {
	        return new QueryRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.requestId = source["requestId"];
	        this.handle = source["handle"];
	        this.filter = this.convertValues(source["filter"], Filter);
	        this.transform = this.convertValues(source["transform"], Transform);
	        this.offset = source["offset"];
	        this.limit = source["limit"];
	        this.wantTotal = source["wantTotal"];
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
	export class Row {
	    index: number;
	    cells: Cell[];
	
	    static createFrom(source: any = {}) {
	        return new Row(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.index = source["index"];
	        this.cells = this.convertValues(source["cells"], Cell);
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
	export class RowSet {
	    columns: Column[];
	    rows: Row[];
	    offset: number;
	    total: number;
	    totalExact: boolean;
	    scanned: number;
	    truncated: boolean;
	    elapsedMs: number;
	    columnsTruncated: boolean;
	    totalPaths: number;
	
	    static createFrom(source: any = {}) {
	        return new RowSet(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.columns = this.convertValues(source["columns"], Column);
	        this.rows = this.convertValues(source["rows"], Row);
	        this.offset = source["offset"];
	        this.total = source["total"];
	        this.totalExact = source["totalExact"];
	        this.scanned = source["scanned"];
	        this.truncated = source["truncated"];
	        this.elapsedMs = source["elapsedMs"];
	        this.columnsTruncated = source["columnsTruncated"];
	        this.totalPaths = source["totalPaths"];
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

export namespace visual {
	
	export class TypeSegment {
	    kind: string;
	    label: string;
	    frac: number;
	    offset: number;
	    count: number;
	    percent: number;
	    series: number;
	
	    static createFrom(source: any = {}) {
	        return new TypeSegment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.frac = source["frac"];
	        this.offset = source["offset"];
	        this.count = source["count"];
	        this.percent = source["percent"];
	        this.series = source["series"];
	    }
	}
	export class ArrayBreakdown {
	    elementPath: string;
	    present: boolean;
	    elementCount: number;
	    elementTypes: TypeSegment[];
	
	    static createFrom(source: any = {}) {
	        return new ArrayBreakdown(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.elementPath = source["elementPath"];
	        this.present = source["present"];
	        this.elementCount = source["elementCount"];
	        this.elementTypes = this.convertValues(source["elementTypes"], TypeSegment);
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
	export class Badge {
	    severity: string;
	    code: string;
	    icon: string;
	    label: string;
	    detail: string;
	    path?: string;
	
	    static createFrom(source: any = {}) {
	        return new Badge(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.severity = source["severity"];
	        this.code = source["code"];
	        this.icon = source["icon"];
	        this.label = source["label"];
	        this.detail = source["detail"];
	        this.path = source["path"];
	    }
	}
	export class CategoryBar {
	    label: string;
	    count: number;
	    frac: number;
	    percent: number;
	
	    static createFrom(source: any = {}) {
	        return new CategoryBar(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.count = source["count"];
	        this.frac = source["frac"];
	        this.percent = source["percent"];
	    }
	}
	export class Categorical {
	    bars: CategoryBar[];
	    other?: CategoryBar;
	    total: number;
	    maxCount: number;
	    truncated: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Categorical(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.bars = this.convertValues(source["bars"], CategoryBar);
	        this.other = this.convertValues(source["other"], CategoryBar);
	        this.total = source["total"];
	        this.maxCount = source["maxCount"];
	        this.truncated = source["truncated"];
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
	
	export class DiffDetail {
	    reason: string;
	    message: string;
	    old: string;
	    new: string;
	    breaking: boolean;
	    severity: string;
	
	    static createFrom(source: any = {}) {
	        return new DiffDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.reason = source["reason"];
	        this.message = source["message"];
	        this.old = source["old"];
	        this.new = source["new"];
	        this.breaking = source["breaking"];
	        this.severity = source["severity"];
	    }
	}
	export class DiffRow {
	    path: string;
	    kind: string;
	    breaking: boolean;
	    severity: string;
	    icon: string;
	    label: string;
	    details: DiffDetail[];
	
	    static createFrom(source: any = {}) {
	        return new DiffRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.kind = source["kind"];
	        this.breaking = source["breaking"];
	        this.severity = source["severity"];
	        this.icon = source["icon"];
	        this.label = source["label"];
	        this.details = this.convertValues(source["details"], DiffDetail);
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
	export class DiffGroup {
	    kind: string;
	    label: string;
	    count: number;
	    rows: DiffRow[];
	
	    static createFrom(source: any = {}) {
	        return new DiffGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.label = source["label"];
	        this.count = source["count"];
	        this.rows = this.convertValues(source["rows"], DiffRow);
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
	
	export class KPITile {
	    key: string;
	    label: string;
	    value: string;
	    raw: number;
	    sub?: string;
	    severity?: string;
	    hero?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new KPITile(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.value = source["value"];
	        this.raw = source["raw"];
	        this.sub = source["sub"];
	        this.severity = source["severity"];
	        this.hero = source["hero"];
	    }
	}
	export class DiffVisualModel {
	    old: string;
	    new: string;
	    breaking: boolean;
	    verdict: string;
	    verdictSeverity: string;
	    kpis: KPITile[];
	    groups: DiffGroup[];
	    badges: Badge[];
	    caveats?: string[];
	
	    static createFrom(source: any = {}) {
	        return new DiffVisualModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.old = source["old"];
	        this.new = source["new"];
	        this.breaking = source["breaking"];
	        this.verdict = source["verdict"];
	        this.verdictSeverity = source["verdictSeverity"];
	        this.kpis = this.convertValues(source["kpis"], KPITile);
	        this.groups = this.convertValues(source["groups"], DiffGroup);
	        this.badges = this.convertValues(source["badges"], Badge);
	        this.caveats = source["caveats"];
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
	export class SparkPoint {
	    x: number;
	    y: number;
	
	    static createFrom(source: any = {}) {
	        return new SparkPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.x = source["x"];
	        this.y = source["y"];
	    }
	}
	export class StrLenBar {
	    min: number;
	    max: number;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new StrLenBar(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.min = source["min"];
	        this.max = source["max"];
	        this.text = source["text"];
	    }
	}
	export class HighCardString {
	    distinct: number;
	    distinctText: string;
	    uniqueRatio: number;
	    sample: string[];
	    strLen?: StrLenBar;
	
	    static createFrom(source: any = {}) {
	        return new HighCardString(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.distinct = source["distinct"];
	        this.distinctText = source["distinctText"];
	        this.uniqueRatio = source["uniqueRatio"];
	        this.sample = source["sample"];
	        this.strLen = this.convertValues(source["strLen"], StrLenBar);
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
	export class HistBar {
	    lo: number;
	    hi: number;
	    count: number;
	    frac: number;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new HistBar(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.lo = source["lo"];
	        this.hi = source["hi"];
	        this.count = source["count"];
	        this.frac = source["frac"];
	        this.label = source["label"];
	    }
	}
	export class Histogram {
	    min: number;
	    max: number;
	    binWidth: number;
	    bins: HistBar[];
	    maxCount: number;
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new Histogram(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.min = source["min"];
	        this.max = source["max"];
	        this.binWidth = source["binWidth"];
	        this.bins = this.convertValues(source["bins"], HistBar);
	        this.maxCount = source["maxCount"];
	        this.total = source["total"];
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
	export class Stat {
	    key: string;
	    label: string;
	    text: string;
	    approx?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Stat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.label = source["label"];
	        this.text = source["text"];
	        this.approx = source["approx"];
	    }
	}
	export class Meter {
	    presenceRate: number;
	    nullRate: number;
	    presenceText: string;
	    nullText: string;
	    nullStatus?: string;
	
	    static createFrom(source: any = {}) {
	        return new Meter(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.presenceRate = source["presenceRate"];
	        this.nullRate = source["nullRate"];
	        this.presenceText = source["presenceText"];
	        this.nullText = source["nullText"];
	        this.nullStatus = source["nullStatus"];
	    }
	}
	export class FieldCard {
	    path: string;
	    displayName: string;
	    form: string;
	    kind: string;
	    enumLike: boolean;
	    arrayElement: boolean;
	    observations: number;
	    status: string;
	    typeMix: TypeSegment[];
	    meter: Meter;
	    stats: Stat[];
	    badges: Badge[];
	    histogram?: Histogram;
	    categorical?: Categorical;
	    highCard?: HighCardString;
	    array?: ArrayBreakdown;
	    sparkline?: SparkPoint[];
	
	    static createFrom(source: any = {}) {
	        return new FieldCard(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.displayName = source["displayName"];
	        this.form = source["form"];
	        this.kind = source["kind"];
	        this.enumLike = source["enumLike"];
	        this.arrayElement = source["arrayElement"];
	        this.observations = source["observations"];
	        this.status = source["status"];
	        this.typeMix = this.convertValues(source["typeMix"], TypeSegment);
	        this.meter = this.convertValues(source["meter"], Meter);
	        this.stats = this.convertValues(source["stats"], Stat);
	        this.badges = this.convertValues(source["badges"], Badge);
	        this.histogram = this.convertValues(source["histogram"], Histogram);
	        this.categorical = this.convertValues(source["categorical"], Categorical);
	        this.highCard = this.convertValues(source["highCard"], HighCardString);
	        this.array = this.convertValues(source["array"], ArrayBreakdown);
	        this.sparkline = this.convertValues(source["sparkline"], SparkPoint);
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
	
	
	
	
	
	
	
	
	export class Summary {
	    name: string;
	    format: string;
	    records: number;
	    skipped: number;
	    fieldCount: number;
	    warningCount: number;
	    healthScore: number;
	    healthGrade: string;
	    healthSeverity: string;
	
	    static createFrom(source: any = {}) {
	        return new Summary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.format = source["format"];
	        this.records = source["records"];
	        this.skipped = source["skipped"];
	        this.fieldCount = source["fieldCount"];
	        this.warningCount = source["warningCount"];
	        this.healthScore = source["healthScore"];
	        this.healthGrade = source["healthGrade"];
	        this.healthSeverity = source["healthSeverity"];
	    }
	}
	
	export class VisualModel {
	    summary: Summary;
	    kpis: KPITile[];
	    fields: FieldCard[];
	    badges: Badge[];
	
	    static createFrom(source: any = {}) {
	        return new VisualModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.summary = this.convertValues(source["summary"], Summary);
	        this.kpis = this.convertValues(source["kpis"], KPITile);
	        this.fields = this.convertValues(source["fields"], FieldCard);
	        this.badges = this.convertValues(source["badges"], Badge);
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

