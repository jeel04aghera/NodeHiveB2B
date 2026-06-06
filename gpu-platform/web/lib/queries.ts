import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "./api-client";

export interface NodeSummary {
  id: string;
  hostname: string;
  status: string;
  os?: string;
  nvidia_driver?: string;
  cuda_version?: string;
  agent_version?: string;
  gpu_count: number;
  enrolled_at?: string;
  last_seen_at: string | null;
}

export interface GPU {
  id: string;
  node_id: string;
  index: number;
  uuid: string;
  model: string;
  memory_mb: number;
  mig_enabled: boolean;
  status: string;
  synthetic?: boolean;
  updated_at: string;
}

export interface FleetSummary {
  gpu_total: number;
  gpus_idle: number;
  avg_util_pct: number;
  idle_cost_24h: number;
  workloads_active: number;
}

export interface Workload {
  id: string;
  name: string;
  image: string;
  status: string;
  stage?: string;
  requested_gpu_count: number;
  node_id?: string;
  project_id?: string;
  department_id?: string;
  template_id?: string;
  container_id?: string;
  expose_ssh: boolean;
  expose_jupyter: boolean;
  ssh_endpoint?: string | null;
  ssh_password?: string | null;
  jupyter_endpoint?: string | null;
  logs?: string | null;
  started_at?: string | null;
  stopped_at?: string | null;
  created_at: string;
}

export interface WorkloadGPUAlloc { uuid: string; model: string }

export interface WorkloadDetail extends Workload {
  owner?: string;
  department?: string;
  template?: string;
  template_version?: string;
  gpus?: WorkloadGPUAlloc[] | null;
  runtime_seconds?: number;
  runtime_cost?: number;
  currency?: string;
}

export interface TemplateSoftware { name: string; version: string }
export interface Template {
  id: string;
  org_id?: string;
  name: string;
  description: string;
  base_image: string;
  software: TemplateSoftware[];
  version: string;
  tags: string[];
  default_expose_ssh: boolean;
  default_expose_jupyter: boolean;
  built_in: boolean;
  created_at: string;
}

export interface Department {
  id: string;
  org_id: string;
  name: string;
  description: string;
  created_at: string;
  user_count: number;
  workload_count: number;
}

export interface DeploymentConfig {
  dev_mode: boolean;
  synthetic_gpu_count: number;
  total_gpu_count: number;
  disclaimer: string;
}

export interface EnrollmentToken {
  id: string;
  description: string;
  max_uses: number;
  uses: number;
  expires_at: string;
  last_used_at?: string | null;
  revoked_at?: string | null;
  created_at: string;
  status: string;
}

export interface NodeDetail {
  id: string;
  hostname: string;
  status: string;
  health: string;
  os: string;
  kernel: string;
  cpu_model: string;
  cpu_cores: number;
  ram_mb: number;
  nvidia_driver: string;
  cuda_version: string;
  agent_version: string;
  enrolled_at: string;
  last_seen_at: string | null;
  synthetic: boolean;
  gpu_count: number;
  gpus: GPU[];
  running_workloads: { id: string; name: string; status: string; user_email: string; gpu_count: number }[];
}

export interface UtilPoint {
  ts: string;
  util_pct: number;
  mem_pct: number;
}

export interface RateCard {
  id: string;
  gpu_model: string;
  rate_per_gpu_hour: number;
  currency: string;
  effective_from: string;
  effective_to?: string | null;
}

export interface ChargebackRow {
  group_key: string;
  gpu_hours: number;
  amount: number;
  currency: string;
  util_pct: number;
}

export interface ChargebackReport {
  from: string;
  to: string;
  group_by: string;
  currency: string;
  total: number;
  coverage_pct: number;
  rows: ChargebackRow[] | null;
}

export interface User {
  id: string;
  org_id: string;
  email: string;
  name: string;
  role: string;
  department_id?: string;
}

export interface LedgerEntry {
  id: string;
  delta: number;
  balance: number;
  kind: "grant" | "topup" | "charge" | "adjustment";
  description: string;
  workload_id?: string;
  created_at: string;
}

export interface CreditSummary {
  balance: number;
  total_granted: number;
  total_spent: number;
  month_spent: number;
  recent_entries: LedgerEntry[];
}

export function useNodes() {
  return useQuery({ queryKey: ["nodes"], queryFn: () => api<NodeSummary[]>("/nodes"), refetchInterval: 5_000 });
}

export function useGPUs(status?: string) {
  const qs = status ? `?status=${status}` : "";
  return useQuery({
    queryKey: ["gpus", status ?? "all"],
    queryFn: () => api<GPU[]>(`/gpus${qs}`),
  });
}

export function useNodeGPUs(nodeId: string) {
  return useQuery({
    queryKey: ["nodes", nodeId, "gpus"],
    queryFn: () => api<GPU[]>(`/nodes/${nodeId}/gpus`),
    enabled: !!nodeId,
  });
}

export function useFleetSummary() {
  return useQuery({
    queryKey: ["metrics", "summary"],
    queryFn: () => api<FleetSummary>("/metrics/summary"),
    refetchInterval: 5_000,
  });
}

export function useUtilization(scope: string, id?: string, from?: string, to?: string) {
  const params = new URLSearchParams({ scope });
  if (id) params.set("id", id);
  if (from) params.set("from", from);
  if (to) params.set("to", to);
  return useQuery({
    queryKey: ["metrics", "utilization", scope, id, from, to],
    queryFn: () => api<UtilPoint[]>(`/metrics/utilization?${params}`),
    refetchInterval: 60_000,
  });
}

export function useWorkloads(status?: string) {
  const qs = status ? `?status=${status}` : "";
  return useQuery({
    queryKey: ["workloads", status ?? "all"],
    queryFn: () => api<Workload[]>(`/workloads${qs}`),
    refetchInterval: 5_000,
  });
}

export function useLaunchWorkload() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: Record<string, unknown>) =>
      api<Workload>("/workloads", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["workloads"] }),
  });
}

export function useStopWorkload() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/workloads/${id}/stop`, { method: "POST" }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["workloads"] });
      qc.invalidateQueries({ queryKey: ["metrics"] });
    },
  });
}

// ── Sessions (Phase 2: active-device management) ──────────────────────────────
export interface Session {
  id: string;
  user_id: string;
  device_name: string;
  browser: string;
  os: string;
  ip_address: string;
  user_agent: string;
  created_at: string;
  last_active_at: string;
  expires_at: string;
  revoked_at?: string | null;
  current: boolean;
}

export function useSessions() {
  return useQuery({
    queryKey: ["auth", "sessions"],
    queryFn: () => api<Session[]>("/auth/sessions"),
  });
}

export function useRevokeSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/auth/sessions/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["auth", "sessions"] }),
  });
}

export function useRevokeAllSessions() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api<void>("/auth/sessions/revoke-all", { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["auth", "sessions"] }),
  });
}

// ── Organization members / invitations / join codes (Phase 3) ─────────────────
export interface Member {
  id: string;
  org_id: string;
  user_id: string;
  email: string;
  name: string;
  role: "owner" | "admin" | "member";
  avatar_url?: string;
  invited_by?: string | null;
  created_at: string;
  last_login_at?: string | null;
}
export interface Invitation {
  id: string;
  org_id: string;
  email: string;
  role: "admin" | "member";
  created_at: string;
  expires_at: string;
  status: "pending" | "accepted" | "expired" | "revoked";
  delivery_status: "pending" | "sent" | "failed" | "skipped";
  delivery_error?: string;
  delivered_at?: string | null;
}
export interface JoinCode {
  id: string;
  org_id: string;
  description: string;
  max_uses: number;
  uses: number;
  expires_at: string;
  status: "active" | "expired" | "revoked" | "exhausted";
  created_at: string;
}

export function useMembers() {
  return useQuery({ queryKey: ["org", "members"], queryFn: () => api<Member[]>("/org/members") });
}
export function useChangeMemberRole() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ userId, role }: { userId: string; role: string }) =>
      api<void>(`/org/members/${userId}/role`, { method: "PATCH", body: JSON.stringify({ role }) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["org", "members"] }),
  });
}
export function useRemoveMember() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) => api<void>(`/org/members/${userId}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["org", "members"] }),
  });
}

export function useTransferOwnership() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (userId: string) =>
      api<void>("/org/transfer-ownership", { method: "POST", body: JSON.stringify({ user_id: userId }) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["org", "members"] }),
  });
}

export function useInvitations() {
  return useQuery({ queryKey: ["org", "invitations"], queryFn: () => api<Invitation[]>("/org/invitations") });
}
export interface InviteResult { invitation: Invitation; token: string; accept_url: string }
export function useCreateInvitation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; role: string }) =>
      api<InviteResult>("/org/invitations", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["org", "invitations"] }),
  });
}
export function useResendInvitation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<InviteResult>(`/org/invitations/${id}/resend`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["org", "invitations"] }),
  });
}
export function useRevokeInvitation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/org/invitations/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["org", "invitations"] }),
  });
}

export function useJoinCodes() {
  return useQuery({ queryKey: ["org", "join-codes"], queryFn: () => api<JoinCode[]>("/org/join-codes") });
}
export function useCreateJoinCode() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { description: string; ttl_days: number; max_uses: number }) =>
      api<{ code: string }>("/org/join-codes", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["org", "join-codes"] }),
  });
}
export function useRevokeJoinCode() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/org/join-codes/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["org", "join-codes"] }),
  });
}

export function useRates() {
  return useQuery({
    queryKey: ["billing", "rates"],
    queryFn: () => api<RateCard[]>("/billing/rates"),
  });
}

export function useSetRate() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { gpu_model: string; rate_per_gpu_hour: number; currency: string }) =>
      api<RateCard>("/billing/rates", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["billing", "rates"] }),
  });
}

export function useCredits() {
  return useQuery({
    queryKey: ["billing", "credits"],
    queryFn: () => api<CreditSummary>("/billing/credits"),
    refetchInterval: 15_000,
  });
}

export function useLedger(limit = 50) {
  return useQuery({
    queryKey: ["billing", "ledger", limit],
    queryFn: () => api<LedgerEntry[]>(`/billing/ledger?limit=${limit}`),
  });
}

export function useTopUp() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { amount: number; description?: string }) =>
      api<{ balance: number }>("/billing/credits/topup", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["billing", "credits"] });
      qc.invalidateQueries({ queryKey: ["billing", "ledger"] });
    },
  });
}

export function useChargeback(from: string, to: string, groupBy: string) {
  return useQuery({
    queryKey: ["billing", "chargeback", from, to, groupBy],
    queryFn: () =>
      api<ChargebackReport>(
        `/billing/chargeback?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}&group_by=${groupBy}`,
      ),
    enabled: !!from && !!to,
  });
}

export function useUsers() {
  return useQuery({ queryKey: ["users"], queryFn: () => api<User[]>("/users") });
}

export function useCreateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { email: string; name: string; role: string; department_id?: string }) =>
      api<User>("/users", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
  });
}

export function useAssignUserDepartment() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ userId, departmentId }: { userId: string; departmentId: string | null }) =>
      api<void>(`/users/${userId}/department`, {
        method: "PATCH",
        body: JSON.stringify({ department_id: departmentId }),
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["users"] });
      qc.invalidateQueries({ queryKey: ["departments"] });
    },
  });
}

export function useIssueEnrollmentToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body?: { description?: string; ttl_days?: number; max_uses?: number }) =>
      api<{ token: string }>("/enrollment-tokens", { method: "POST", body: JSON.stringify(body ?? {}) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["enrollment-tokens"] }),
  });
}

export function useEnrollmentTokens() {
  return useQuery({
    queryKey: ["enrollment-tokens"],
    queryFn: () => api<EnrollmentToken[]>("/enrollment-tokens"),
    refetchInterval: 10_000,
  });
}

export function useRevokeToken() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/enrollment-tokens/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["enrollment-tokens"] }),
  });
}

// ── Templates / Departments / Config / Detail ────────────────────────────────

export function useTemplates() {
  return useQuery({ queryKey: ["templates"], queryFn: () => api<Template[]>("/templates") });
}

export function useDepartments() {
  return useQuery({ queryKey: ["departments"], queryFn: () => api<Department[]>("/departments") });
}

export interface Project { id: string; name: string }

export function useProjects() {
  return useQuery({ queryKey: ["projects"], queryFn: () => api<Project[]>("/projects") });
}

export function useCreateProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string }) => api<Project>("/projects", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["projects"] }),
  });
}

export function useCreateDepartment() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; description: string }) =>
      api<Department>("/departments", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["departments"] }),
  });
}

export function useDeploymentConfig() {
  return useQuery({
    queryKey: ["config"],
    queryFn: () => api<DeploymentConfig>("/config"),
    staleTime: 60_000,
  });
}

export function useNodeDetail(id: string) {
  return useQuery({
    queryKey: ["nodes", id, "detail"],
    queryFn: () => api<NodeDetail>(`/nodes/${id}`),
    enabled: !!id,
    refetchInterval: 5_000,
  });
}

export function useWorkloadDetail(id: string) {
  return useQuery({
    queryKey: ["workloads", id, "detail"],
    queryFn: () => api<WorkloadDetail>(`/workloads/${id}`),
    enabled: !!id,
    refetchInterval: 5_000,
  });
}

export interface AuditEvent {
  id: number;
  actor_type: string;
  actor_id: string;
  action: string;
  target_type: string;
  target_id: string;
  metadata: Record<string, unknown>;
  ts: string;
}

export function useAuditLogs(from: string, to: string) {
  return useQuery({
    queryKey: ["audit-logs", from, to],
    queryFn: () =>
      api<AuditEvent[]>(
        `/audit-logs?from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`,
      ),
    refetchInterval: 5_000,
  });
}

// F1 — workload lifecycle event (deployment timeline), distinct from audit logs.
export interface LifecycleEvent {
  id: number;
  stage: string;
  message: string;
  ts: string;
}

export function useWorkloadEvents(workloadId: string) {
  return useQuery({
    queryKey: ["workloads", workloadId, "events"],
    queryFn: () => api<LifecycleEvent[]>(`/workloads/${workloadId}/events`),
    enabled: !!workloadId,
    refetchInterval: 5_000,
  });
}

export function useWorkloadLogs(workloadId: string, enabled: boolean) {
  return useQuery({
    queryKey: ["workloads", workloadId, "logs"],
    queryFn: () => api<{ logs: string }>(`/workloads/${workloadId}/logs`),
    enabled: enabled && !!workloadId,
    refetchInterval: 5_000,
  });
}

// ── F3: GPU queue ─────────────────────────────────────────────────────────────

export interface QueueEntry {
  id: string;
  name: string;
  position: number;
  gpu_type: string;
  gpu_count: number;
  queued_at: string;
  est_wait_min: number;
  est_start: string;
  owner_email?: string;
  project_name?: string;
}

export interface QueueStats {
  waiting: number;
  avg_wait_min: number;
  next_free_at?: string | null;
  entries: QueueEntry[];
}

export function useQueue() {
  return useQuery({
    queryKey: ["queue"],
    queryFn: () => api<QueueStats>("/queue"),
    refetchInterval: 5_000,
  });
}

// ── F4: Reservations ──────────────────────────────────────────────────────────

export interface Reservation {
  id: string;
  gpu_model: string;
  gpu_count: number;
  start_at: string;
  end_at: string;
  status: "upcoming" | "active" | "expired" | "cancelled";
  project_name?: string;
  owner_email?: string;
  created_at: string;
}

export function useReservations() {
  return useQuery({
    queryKey: ["reservations"],
    queryFn: () => api<Reservation[]>("/reservations"),
    refetchInterval: 15_000,
  });
}

export function useCreateReservation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      gpu_model: string;
      gpu_count: number;
      start_at: string;
      end_at: string;
      project_id?: string;
    }) => api<Reservation>("/reservations", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["reservations"] }),
  });
}

export function useCancelReservation() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/reservations/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["reservations"] }),
  });
}

// ── F6: Budgets ───────────────────────────────────────────────────────────────

export interface Budget {
  id: string;
  scope_type: "organization" | "department" | "project";
  scope_id?: string;
  scope_name: string;
  amount: number;
  spend: number;
  forecast: number;
  remaining: number;
  burn_per_day: number;
  used_pct: number;
  status: "safe" | "at_risk" | "exceeded";
}

export function useBudgets() {
  return useQuery({
    queryKey: ["budgets"],
    queryFn: () => api<Budget[]>("/budgets"),
    refetchInterval: 15_000,
  });
}

export function useSetBudget() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { scope_type: string; scope_id?: string; amount: number }) =>
      api<void>("/budgets", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["budgets"] }),
  });
}

export function useDeleteBudget() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/budgets/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["budgets"] }),
  });
}

// ── F5: Cost alerts ───────────────────────────────────────────────────────────

export type AlertType =
  | "project_spend"
  | "department_spend"
  | "workload_runtime"
  | "idle_workload"
  | "budget_utilization";

export interface AlertRule {
  id: string;
  type: AlertType;
  threshold: number;
  scope_id?: string;
  scope_name?: string;
  severity: "info" | "warning" | "critical";
  enabled: boolean;
  created_at: string;
}

export interface Alert {
  id: string;
  rule_id?: string;
  severity: "info" | "warning" | "critical";
  title: string;
  message: string;
  status: "active" | "acknowledged";
  created_at: string;
}

export function useAlerts(includeAcked = false) {
  return useQuery({
    queryKey: ["alerts", includeAcked],
    queryFn: () => api<Alert[]>(`/alerts${includeAcked ? "?all=true" : ""}`),
    refetchInterval: 15_000,
  });
}

export function useAckAlert() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/alerts/${id}/ack`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["alerts"] }),
  });
}

export function useAlertRules() {
  return useQuery({
    queryKey: ["alert-rules"],
    queryFn: () => api<AlertRule[]>("/alert-rules"),
  });
}

export function useCreateAlertRule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { type: string; threshold: number; scope_id?: string; severity: string }) =>
      api<{ id: string }>("/alert-rules", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["alert-rules"] }),
  });
}

export function useToggleAlertRule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      api<void>(`/alert-rules/${id}`, { method: "PATCH", body: JSON.stringify({ enabled }) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["alert-rules"] }),
  });
}

export function useDeleteAlertRule() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api<void>(`/alert-rules/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["alert-rules"] }),
  });
}
