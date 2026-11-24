import { createContext, useContext, useMemo, useState, type ReactNode } from "react";
import { OwnerClient } from "@asa/sdk";

export interface Session {
  controlPlaneUrl: string;
  apiKey: string;
}

interface SessionCtx {
  session: Session | null;
  client: OwnerClient | null;
  signIn: (s: Session) => void;
  signOut: () => void;
}

const Ctx = createContext<SessionCtx>({ session: null, client: null, signIn: () => {}, signOut: () => {} });
const KEY = "asa.owner.session";

export function SessionProvider({ children }: { children: ReactNode }) {
  const [session, setSession] = useState<Session | null>(() => {
    try {
      const raw = sessionStorage.getItem(KEY);
      return raw ? (JSON.parse(raw) as Session) : null;
    } catch {
      return null;
    }
  });
  const client = useMemo(() => (session ? new OwnerClient({ controlPlaneUrl: session.controlPlaneUrl, apiKey: session.apiKey }) : null), [session]);
  const value = useMemo<SessionCtx>(
    () => ({
      session,
      client,
      signIn: (s) => {
        sessionStorage.setItem(KEY, JSON.stringify(s));
        setSession(s);
      },
      signOut: () => {
        sessionStorage.removeItem(KEY);
        setSession(null);
      },
    }),
    [session, client],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useSession(): SessionCtx {
  return useContext(Ctx);
}

export function useClient(): OwnerClient {
  const { client } = useSession();
  if (!client) throw new Error("not signed in");
  return client;
}
