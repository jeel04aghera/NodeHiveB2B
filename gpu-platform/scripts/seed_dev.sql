-- Dev seed data. Loaded by `make seed`. Idempotent (safe to re-run).
-- NOTE: the admin USER is not seeded here. The control plane bootstraps it on first
-- run from DEV_BOOTSTRAP_ADMIN (see .env.example), so we never ship a password hash.

-- Organization (single-org dev)
INSERT INTO organizations (id, name, slug, settings) VALUES
 ('00000000-0000-0000-0000-000000000001', 'Dev Org', 'dev',
  '{"idle_threshold_pct": 5, "idle_duration_min": 30, "currency": "USD", "default_rate": 0.50}')
ON CONFLICT (slug) DO NOTHING;

-- Projects (chargeback cost centers)
INSERT INTO projects (org_id, name) VALUES
 ('00000000-0000-0000-0000-000000000001', 'research'),
 ('00000000-0000-0000-0000-000000000001', 'platform'),
 ('00000000-0000-0000-0000-000000000001', 'unallocated')
ON CONFLICT (org_id, name) DO NOTHING;

-- Departments (org structure — workloads & users belong to a department)
INSERT INTO departments (org_id, name, description) VALUES
 ('00000000-0000-0000-0000-000000000001', 'AI',          'Applied AI & model training'),
 ('00000000-0000-0000-0000-000000000001', 'Research',    'ML research & experimentation'),
 ('00000000-0000-0000-0000-000000000001', 'Design',      'Generative & creative tooling'),
 ('00000000-0000-0000-0000-000000000001', 'Engineering', 'Platform & infrastructure')
ON CONFLICT (org_id, name) DO NOTHING;

-- Rate cards (so chargeback has prices). Fixed ids for idempotency.
INSERT INTO rate_cards (id, org_id, gpu_model, rate_per_gpu_hour, currency) VALUES
 ('00000000-0000-0000-0000-0000000000c1', '00000000-0000-0000-0000-000000000001', 'NVIDIA RTX 4090',     0.34, 'USD'),
 ('00000000-0000-0000-0000-0000000000c2', '00000000-0000-0000-0000-000000000001', 'NVIDIA RTX 6000 Ada', 0.69, 'USD'),
 ('00000000-0000-0000-0000-0000000000c3', '00000000-0000-0000-0000-000000000001', 'NVIDIA A100',         0.89, 'USD'),
 ('00000000-0000-0000-0000-0000000000c4', '00000000-0000-0000-0000-000000000001', 'NVIDIA H100',         2.69, 'USD')
ON CONFLICT (id) DO NOTHING;

-- NOTE: no fake nodes/GPUs are seeded. Nodes and GPUs appear only when a real
-- agent enrolls and reports inventory (run `make agent`, or `--dev` on a box
-- without NVIDIA hardware). This keeps the fleet view honest — every row is a
-- node that actually connected.
