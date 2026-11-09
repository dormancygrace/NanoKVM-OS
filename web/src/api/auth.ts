import { http } from '@/lib/http';

export type UserRole = 'admin' | 'user';

export type Account = {
  username: string;
  role: UserRole;
  // The server only answers password-change routes until this is cleared.
  mustChangePassword?: boolean;
  // Two-factor authentication; absent on older servers.
  totpEnabled?: boolean;
  recoveryCodesLeft?: number;
};

export type User = Omit<Account, 'mustChangePassword' | 'recoveryCodesLeft'> & {
  enabled: boolean;
  systemAccount?: boolean;
};

// The login answer of an account with two-factor authentication: finish it
// with loginTOTP within five minutes.
export const LOGIN_TOTP_REQUIRED = -6;
// The device clock is not set, so codes cannot be checked (recovery codes can).
export const TOTP_CLOCK_NOT_SET = -7;
// The challenge expired, ran out of attempts or the account changed.
export const LOGIN_EXPIRED = -8;

export function login(username: string, password: string) {
  const data = {
    username,
    passwordEncoding: 'plain',
    password
  };
  return http.post<{ token: string } | { challenge: string } | null>('/api/auth/login', data, {
    sensitive: true
  });
}

export function loginTOTP(challenge: string, otp: string) {
  return http.post<null>('/api/auth/login/totp', { challenge, otp }, { sensitive: true });
}

export function totpSetup() {
  return http.post<{ secret: string; uri: string }>('/api/auth/totp/setup', null, {
    sensitive: true
  });
}

export function totpEnable(otp: string) {
  return http.post<{ recoveryCodes: string[] }>(
    '/api/auth/totp/enable',
    { otp },
    { sensitive: true }
  );
}

export function totpDisable(password: string, otp: string) {
  return http.post<null>(
    '/api/auth/totp/disable',
    { password, otp, passwordEncoding: 'plain' },
    { sensitive: true }
  );
}

export function totpRecoveryCodes(password: string, otp: string) {
  return http.post<{ recoveryCodes: string[] }>(
    '/api/auth/totp/recovery-codes',
    { password, otp, passwordEncoding: 'plain' },
    { sensitive: true }
  );
}

export function resetUserTOTP(username: string) {
  return http.delete<null>(`/api/auth/users/${encodeURIComponent(username)}/totp`);
}

export function logout() {
  return http.post<null>('/api/auth/logout');
}

export function getAccount() {
  return http.get<Account>('/api/auth/account');
}

// A forced change of the factory password sends no current password and
// returns signedIn: the browser has a new session.
export function changePassword(currentPassword: string, password: string) {
  return http.post<{ signedIn?: boolean } | null>(
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
