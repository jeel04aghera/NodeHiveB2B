"use client";
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { api, setToken, ApiError } from "./api-client";

export interface CurrentUser {
  id: string;
  org_id: string;
  email: string;
  name: string;
  role: string;
}

interface AuthCtx {
  user: CurrentUser | null;
  ready: boolean;
  login: (email: string, password: string) => Promise<void>;
  register: (input: { orgName: string; name: string; email: string; password: string }) => Promise<void>;
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

  function logout() {
    localStorage.removeItem(KEY);
    setToken(null);
    setUser(null);
  }

  return (
    <Ctx.Provider value={{ user, ready, login, register, logout }}>
      {children}
    </Ctx.Provider>
  );
}

export function useAuth() {
  const c = useContext(Ctx);
  if (!c) throw new Error("useAuth outside AuthProvider");
  return c;
}
