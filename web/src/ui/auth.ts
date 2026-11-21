import { createContext, useContext } from 'react';
import type { User } from '../api';

// Who is using the hub, and what their role lets them do. Mirrors internal/auth/permissions.go:
// the server refuses anyway; the UI just doesn't offer what would only answer 403.
export type Perm = 'apps' | 'databases' | 'nodes' | 'settings' | 'users' | 'any-workspace';
const GRANTS: Record<string, Perm[]> = {
  owner: ['apps', 'databases', 'nodes', 'settings', 'users', 'any-workspace'],
  deployer: ['apps', 'databases'],
};
export const allowed = (me: User | null, p: Perm) => !!me && (GRANTS[me.role] ?? []).includes(p);
export const MeCtx = createContext<User | null>(null);
export function useCan() {
  const me = useContext(MeCtx);
  return {
    me,
    can: (p: Perm) => allowed(me, p),
    // their own workspace, or a role that may touch anyone's
    canUse: (owner: string) => !!me && (me.login === owner || allowed(me, 'any-workspace')),
  };
}
