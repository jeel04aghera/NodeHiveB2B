"use client";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { api, setToken, ApiError } from "./api-client";

export interface CurrentUser {
  id: string;
  org_id: string | null;
  email: string;
  name: string;
  role: string;
  avatar_url?: string;
  auth_provider?: string;
  onboarded?: boolean;
}

interface AuthCtx {
  user: CurrentUser | null;
  ready: boolean;
  login: (email: string, password: string) => Promise<void>;
  register: (input: { orgName: string; name: string; email: string; password: string }) => Promise<void>;
  // loginWithToken adopts a token minted out-of-band (the Google OAuth callback).
  loginWithToken: (token: string) => Promise<CurrentUser>;
  // createOrg provisions an org for a pre-onboarding user and adopts the new session.
  createOrg: (orgName: string) => Promise<CurrentUser>;
  logout: () => void;
}

const Ctx = createContext<AuthCtx | null>(null);
const KEY = "nh_token";

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<CurrentUser | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const tok = localStorage.getItem(KEY);
    if (!tok) { setReady(true); return; }
    setToken(tok);
    api<CurrentUser>("/me")
      .then(setUser)
      .catch((err) => {
        // 401/403 → token is invalid, force re-login
        if (err instanceof ApiError) {
          localStorage.removeItem(KEY);
          setToken(null);
        }
        // Network error (TypeError) → backend down, keep token so next page load retries
      })
      .finally(() => setReady(true));
  }, []);

  async function login(email: string, password: string) {
    const r = await api<{ token: string; user: CurrentUser }>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    });
    localStorage.setItem(KEY, r.token);
    setToken(r.token);
    setUser(r.user);
  }

  async function register(input: { orgName: string; name: string; email: string; password: string }) {
    const r = await api<{ token: string; user: CurrentUser }>("/auth/register", {
      method: "POST",
      body: JSON.stringify({
        org_name: input.orgName,
        name: input.name,
        email: input.email,
        password: input.password,
      }),
    });
    localStorage.setItem(KEY, r.token);
    setToken(r.token);
    setUser(r.user);
  }

  async function loginWithToken(token: string): Promise<CurrentUser> {
    localStorage.setItem(KEY, token);
    setToken(token);
    const u = await api<CurrentUser>("/me");
    setUser(u);
    return u;
  }

  async function createOrg(orgName: string): Promise<CurrentUser> {
    const r = await api<{ token: string; user: CurrentUser }>("/onboarding/organization", {
      method: "POST",
      body: JSON.stringify({ org_name: orgName }),
    });
    localStorage.setItem(KEY, r.token);
    setToken(r.token);
    setUser(r.user);
    return r.user;
  }

  function logout() {
    localStorage.removeItem(KEY);
    setToken(null);
    setUser(null);
  }

  return (
    <Ctx.Provider value={{ user, ready, login, register, loginWithToken, createOrg, logout }}>
      {children}
    </Ctx.Provider>
  );
}

export function useAuth() {
  const c = useContext(Ctx);
  if (!c) throw new Error("useAuth outside AuthProvider");
  return c;
}
