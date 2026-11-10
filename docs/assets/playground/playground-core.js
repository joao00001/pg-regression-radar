// Simulation core of the docs playground. No DOM access, so it runs in a
// browser and in Node (the repo's tests drive it from Node).
//
// A run executes a SQL workload against a real PostgreSQL (PGlite, PostgreSQL
// compiled to WebAssembly) with pg_stat_statements enabled, applies a "deploy"
// halfway through, and snapshots pg_stat_statements on a virtual clock. The
// snapshots are what the collector would have scraped; the WebAssembly build of
// the real correlation engine analyses them.

export const SCRAPE_SECONDS = 30;

export const SETUP_SQL = `CREATE TABLE orders (
  id          serial PRIMARY KEY,
  customer_id int NOT NULL,
  status      text NOT NULL,
  amount      numeric(10,2) NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);
INSERT INTO orders (customer_id, status, amount)
SELECT (random() * 2000)::int,
       (ARRAY['new','paid','shipped','cancelled'])[1 + (random() * 3)::int],
       (random() * 500)::numeric(10,2)
FROM generate_series(1, 30000);
CREATE INDEX orders_customer_idx ON orders (customer_id);
ANALYZE orders;`;

export const WORKLOAD_SQL = `SELECT id, status, amount FROM orders WHERE customer_id = :id
SELECT status, count(*) FROM orders WHERE id BETWEEN :id AND :id + 50 GROUP BY status
UPDATE orders SET status = 'shipped' WHERE id = :id`;

export const SCENARIOS = [
  {
    id: 'drop-index',
    title: 'Deploy removes an index',
    blurb: 'A migration drops the index the customer lookup relies on, so every call turns into a sequential scan.',
    deploySql: 'DROP INDEX orders_customer_idx;',
    expected: 'Detected',
  },
  {
    id: 'harmless',
    title: 'Harmless deploy',
    blurb: 'The deploy adds an unrelated index. Latency is unchanged, so nothing should be reported.',
    deploySql: 'CREATE INDEX orders_created_idx ON orders (created_at);',
    expected: 'NoRegression',
  },
  {
    id: 'reset',
    title: 'Stats reset after a harmless deploy',
    blurb: 'Someone runs pg_stat_statements_reset() right after the deploy. The cumulative mean jumps for no real reason; the collector detects the reset and the interval mean stays flat.',
    deploySql: 'CREATE INDEX orders_created_idx ON orders (created_at);',
    resetAfterDeployScrapes: 3,
    expected: 'NoRegression',
  },
  {
    id: 'trigger',
    title: 'Deploy adds a slow trigger',
    blurb: 'Only the UPDATE gets slower. The report is per query: the other two queries stay clean.',
    deploySql: `CREATE FUNCTION audit() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN PERFORM pg_sleep(0.004); RETURN NEW; END $$;
CREATE TRIGGER orders_audit BEFORE UPDATE ON orders
  FOR EACH ROW EXECUTE FUNCTION audit();`,
    expected: 'Detected',
  },
  {
    id: 'low-traffic',
    title: 'Same regression, almost no traffic',
    blurb: 'The index is dropped, but the queries run too rarely to say anything. The engine refuses to guess (InsufficientData).',
    deploySql: 'DROP INDEX orders_customer_idx;',
    callsPerScrape: 2,
    expected: 'InsufficientData',
  },
];

export const DEFAULTS = {
  setupSql: SETUP_SQL,
  workloadSql: WORKLOAD_SQL,
  callsPerScrape: 10,
  minutesBefore: 12,
  minutesAfter: 10,
  windowMinutes: 10,
  minSamples: 10,
  minExecutions: 100,
  latencyChangeThreshold: 0.2,
  pValueThreshold: 0.05,
  seed: 7,
};

function mulberry32(a) {
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function splitStatements(text) {
  return text.split('\n').map((s) => s.trim()).filter((s) => s && !s.startsWith('--'));
}

// A rough "enough rows" bound for :id substitution, read from the table.
async function idBound(db) {
  try {
    const r = await db.query('SELECT max(id)::int AS m FROM orders');
    return Math.max(1, r.rows[0].m - 60);
  } catch {
    return 1000;
  }
}

const tick = () => new Promise((r) => setTimeout(r, 0));

/**
 * Runs the workload and returns the analysis request.
 * @param {{PGlite: any, pg_stat_statements: any}} pg  PGlite constructor + extension
 * @param {object} params  see DEFAULTS, plus deploySql and resetAfterDeployScrapes
 * @param {(p: {done: number, total: number, phase: string}) => void} [onProgress]
 */
export async function simulate(pg, params, onProgress = () => {}) {
  const p = { ...DEFAULTS, ...params };
  const rand = mulberry32(p.seed);
  const db = new pg.PGlite({ extensions: { pg_stat_statements: pg.pg_stat_statements } });
  try {
    await db.waitReady;
    await db.exec('CREATE EXTENSION IF NOT EXISTS pg_stat_statements');
    onProgress({ done: 0, total: 1, phase: 'Creating the schema' });
    await db.exec(p.setupSql);

    const workload = splitStatements(p.workloadSql);
    if (!workload.length) throw new Error('The workload has no statements.');
    const bound = await idBound(db);
    const render = (sql) => sql.replace(/:id\b/g, () => String(1 + Math.floor(rand() * bound)));

    // Warm up once to learn which queryids belong to the workload, so the
    // playground's own bookkeeping queries never show up as "queries".
    await db.query('SELECT pg_stat_statements_reset()');
    for (const sql of workload) await db.query(render(sql));
    const known = await db.query(
      "SELECT queryid::text AS id FROM pg_stat_statements WHERE calls > 0 AND query NOT ILIKE '%pg_stat_statements%'",
    );
    const workloadIds = new Set(known.rows.map((r) => r.id));
    await db.query('SELECT pg_stat_statements_reset()');

    const scrapes = Math.round(((p.minutesBefore + p.minutesAfter) * 60) / SCRAPE_SECONDS);
    const deployScrape = Math.round((p.minutesBefore * 60) / SCRAPE_SECONDS);
    const startMs = Date.now() - scrapes * SCRAPE_SECONDS * 1000;
    const snapshots = [];
    let deployAt = 0;
    const takeSnapshot = async (i) => {
      const r = await db.query(
        `SELECT queryid::text AS queryid, query, calls::bigint::text AS calls,
                total_exec_time::float8 AS total_exec_time,
                (extract(epoch FROM stats_since) * 1000)::float8 AS stats_since
         FROM pg_stat_statements WHERE calls > 0`,
      );
      snapshots.push({
        at: startMs + i * SCRAPE_SECONDS * 1000,
        rows: r.rows
          .filter((x) => workloadIds.has(x.queryid))
          .map((x) => ({
            queryid: x.queryid, query: x.query, calls: Number(x.calls),
            total_exec_time: x.total_exec_time, stats_since: Math.round(x.stats_since),
          })),
      });
    };

    await takeSnapshot(0);
    for (let i = 1; i <= scrapes; i++) {
      if (i === deployScrape) {
        await db.exec(p.deploySql);
        deployAt = startMs + i * SCRAPE_SECONDS * 1000;
      }
      if (p.resetAfterDeployScrapes && i === deployScrape + p.resetAfterDeployScrapes) {
        await db.query('SELECT pg_stat_statements_reset()');
      }
      for (const sql of workload) {
        for (let k = 0; k < p.callsPerScrape; k++) await db.query(render(sql));
      }
      await takeSnapshot(i);
      onProgress({ done: i, total: scrapes, phase: i < deployScrape ? 'Before the deploy' : 'After the deploy' });
      if (i % 2 === 0) await tick();
    }

    return {
      snapshots,
      deployAt,
      config: {
        windowMinutes: p.windowMinutes,
        minSamples: p.minSamples,
        minExecutions: p.minExecutions,
        latencyChangeThreshold: p.latencyChangeThreshold,
        pValueThreshold: p.pValueThreshold,
      },
    };
  } finally {
    await db.close();
  }
}
