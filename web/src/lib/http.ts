import axios, { AxiosInstance, AxiosRequestConfig } from 'axios';

import { notifyAuthExpired, notifyPasswordChangeRequired } from '@/lib/auth-events.ts';
import { isHandledRequestError, passwordChangeRequiredCode } from '@/lib/request-error.ts';
import { getBaseUrl } from '@/lib/service.ts';

type Response = {
  code: number;
  msg: string;
  data: any;
};

class Http {
  private instance: AxiosInstance;

  constructor() {
    const baseURL = getBaseUrl('http');
    const withCredentials = (import.meta.env.VITE_WITH_CREDENTIALS as string) !== 'false';

    this.instance = axios.create({
      baseURL,
      withCredentials,
      timeout: 60 * 1000
    });

    this.setInterceptors();
  }

  private setInterceptors() {
    this.instance.interceptors.request.use((config) => {
      if (config.headers) {
        config.headers.Accept = 'application/json';
      }

      return config;
    });

    this.instance.interceptors.response.use(
      (response) => {
        return response.data;
      },
      (error) => {
        // Aborts, session expiry and the password redirect are expected.
        if (!isHandledRequestError(error)) console.log(error);
        const code = error.response?.status;
        if (code === 401) {
          notifyAuthExpired();
        } else if (code === 403 && error.response?.data?.code === passwordChangeRequiredCode) {
          // Let the route guard show the forced change, so the page knows
          // why it opened and Cancel cannot lead back here in a loop.
          notifyPasswordChangeRequired();
        }
        return Promise.reject(error);
      }
    );
  }

  public get(url: string, params?: any): Promise<Response> {
    return this.instance.request({
      method: 'get',
      url,
      params
    });
  }

  public post(url: string, data?: any, config?: AxiosRequestConfig): Promise<Response> {
    return this.instance.request({
      method: 'post',
      url,
      data,
      ...config
    });
  }

  public delete(url: string, data?: any): Promise<Response> {
    return this.instance.request({
      method: 'delete',
      url,
      data
    });
  }

  public request(config: AxiosRequestConfig): Promise<Response> {
    return this.instance.request(config);
  }
}

export const http = new Http();
