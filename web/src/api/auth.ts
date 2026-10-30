import { http } from '@/lib/http';

export type UserRole = 'admin' | 'user';

export type Account = {
  username: string;
  role: UserRole;
  // The server only answers password-change routes until this is cleared.
  mustChangePassword?: boolean;
};

export type User = Omit<Account, 'mustChangePassword'> & {
  enabled: boolean;
  systemAccount?: boolean;
};

export function login(username: string, password: string) {
  const data = {
    username,
    passwordEncoding: 'plain',
    password
  };
  return http.post<{ token: string } | null>('/api/auth/login', data, { sensitive: true });
}

export function logout() {
  return http.post<null>('/api/auth/logout');
}

export function getAccount() {
  return http.get<Account>('/api/auth/account');
}

export function changePassword(currentPassword: string, password: string) {
  return http.post<null>(
    '/api/auth/password',
    { currentPassword, password, passwordEncoding: 'plain' },
    { sensitive: true }
  );
}

export function getUsers() {
  return http.get<{ users: User[] }>('/api/auth/users');
}

export function createUser(username: string, password: string, role: UserRole) {
  return http.post<null>(
    '/api/auth/users',
    { username, password, role, passwordEncoding: 'plain' },
    { sensitive: true }
  );
}

export function updateUser(
  username: string,
  data: Partial<Pick<User, 'username' | 'role' | 'enabled'>>
) {
  return http.request<null>({
    method: 'put',
    url: `/api/auth/users/${encodeURIComponent(username)}`,
    data
  });
}

export function deleteUser(username: string) {
  return http.delete<null>(`/api/auth/users/${encodeURIComponent(username)}`);
}

export function resetUserPassword(username: string, password: string) {
  return http.post<null>(
    `/api/auth/users/${encodeURIComponent(username)}/password`,
    { password, passwordEncoding: 'plain' },
    { sensitive: true }
  );
}
