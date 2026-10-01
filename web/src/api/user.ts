import { useEffect } from 'react';
import { useMutation } from '@tanstack/react-query';
import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import { apiRequest, apiUnauthorizedEvent, setAPIKey } from './client';

/**
 * 用户登录请求
 */
interface UserLoginRequest {
    username: string;
    password: string;
    expire: number; // 登录状态过期时间，正数为秒，-1 表示 30 天。
}

/**
 * 认证状态 Store
 */
interface AuthState {
    isAuthenticated: boolean;
    isLoading: boolean;
    isAPIKeyAuth: boolean;
    token: string | null;

    // Actions
    setAuth: () => void;
    setAPIKeyAuth: (apiKey: string) => void;
    checkAuth: () => Promise<void>;
    logout: () => void;
}

/**
 * 认证状态管理 Store（使用 zustand + persist）
 */
export const useAuthStore = create<AuthState>()(
    persist(
        (set, get) => ({
            isAuthenticated: false,
            isLoading: true,
            isAPIKeyAuth: false,
            token: null,

            setAuth: () => {
                setAPIKey(null);
                set({
                    isAuthenticated: true,
                    isAPIKeyAuth: false,
                    token: null,
                    isLoading: false
                });
            },

            setAPIKeyAuth: (apiKey: string) => {
                setAPIKey(apiKey);
                set({
                    isAuthenticated: true,
                    isAPIKeyAuth: true,
                    token: apiKey,
                    isLoading: false
                });
            },

            checkAuth: async () => {
                const { token, isAPIKeyAuth } = get();
                setAPIKey(isAPIKeyAuth ? token : null);

                if (isAPIKeyAuth && !token) {
                    set({ isAuthenticated: false, isLoading: false });
                    return;
                }

                try {
                    const endpoint = isAPIKeyAuth ? '/api/v1/apikey/login' : '/api/v1/user/status';
                    await apiRequest<unknown>(endpoint, { dispatchUnauthorized: false });
                    set({
                        isAuthenticated: true,
                        isLoading: false,
                        token: isAPIKeyAuth ? token : null
                    });
                } catch {
                    get().logout();
                }
            },

            logout: () => {
                setAPIKey(null);
                set({
                    isAuthenticated: false,
                    isAPIKeyAuth: false,
                    token: null,
                    isLoading: false
                });
                if (typeof document !== 'undefined') {
                    const cookiePath = new URL('./', document.baseURI).pathname; // 当前应用所在目录对应的 Cookie 路径。
                    document.cookie = `auth=; Max-Age=0; Path=${cookiePath}; SameSite=Lax`;
                    // 反向代理重写 Cookie Path 时也可省略目录末尾的斜杠。
                    if (cookiePath !== '/') {
                        document.cookie = `auth=; Max-Age=0; Path=${cookiePath.slice(0, -1)}; SameSite=Lax`;
                    }
                }
            }
        }),
        {
            name: 'auth-storage',
            partialize: (state) => ({
                token: state.token,
                isAPIKeyAuth: state.isAPIKeyAuth,
            })
        }
    )
);

/**
 * 用户登录 Hook
 * 
 * @example
 * const login = useLogin();
 * login.mutate({ username: 'admin', password: '123456', expire: 86400 });
 * 
 * if (login.isPending) return <Loading />;
 * if (login.isError) return <Error message={login.error.message} />;
 */
export function useLogin() {
    const { setAuth } = useAuthStore();

    return useMutation({
        mutationFn: async (data: UserLoginRequest) => {
            setAPIKey(null);
            return apiRequest<string>('/api/v1/user/login', {
                method: 'POST',
                body: data,
                dispatchUnauthorized: false,
            });
        },
        onSuccess: () => {
            setAuth();
        },
    });
}

/**
 * 修改密码 Hook
 * 
 * @example
 * const changePassword = useChangePassword();
 * changePassword.mutate({ oldPassword: '123', newPassword: '456' });
 */
export function useChangePassword() {
    return useMutation({
        mutationFn: (data: { oldPassword: string; newPassword: string }) =>
            apiRequest<string>('/api/v1/user/change-password', {
                method: 'POST',
                body: {
                    old_password: data.oldPassword,
                    new_password: data.newPassword,
                },
            }),
    });
}

/**
 * 修改用户名 Hook
 * 
 * @example
 * const changeUsername = useChangeUsername();
 * changeUsername.mutate({ newUsername: 'newname' });
 */
export function useChangeUsername() {
    return useMutation({
        mutationFn: (data: { newUsername: string }) =>
            apiRequest<string>('/api/v1/user/change-username', {
                method: 'POST',
                body: { new_username: data.newUsername },
            }),
    });
}

/**
 * 认证状态和方法 Hook
 * 
 * @example
 * const auth = useAuth();
 * 
 * if (auth.isAuthenticated) {
 *   // 已登录
 * }
 * 
 * auth.logout(); // 登出
 */
export function useAuth() {
    const store = useAuthStore();
    const { checkAuth, isLoading } = store;

    // 只在首次挂载时检查认证状态
    useEffect(() => {
        const handleUnauthorized = () => useAuthStore.getState().logout();
        window.addEventListener(apiUnauthorizedEvent, handleUnauthorized);
        if (isLoading) {
            void checkAuth();
        }
        return () => window.removeEventListener(apiUnauthorizedEvent, handleUnauthorized);
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []); // 有意只在挂载时执行一次

    return {
        isAuthenticated: store.isAuthenticated,
        isAPIKeyAuth: store.isAPIKeyAuth,
        isLoading: store.isLoading,
        logout: store.logout,
    };
}
