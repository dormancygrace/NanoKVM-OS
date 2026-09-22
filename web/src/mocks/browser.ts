import { http, HttpResponse } from 'msw';
import { setupWorker } from 'msw/browser';

let isLoggedIn = false;
let bannerStyle: 'default' | 'rainbow' = 'default';

const branding = () => ({
  logoRevision: '',
  faviconRevision: '',
  customLogoAvailable: false,
  customFaviconAvailable: false,
  buttonColor: '#45E9A0',
  customButtonColor: false,
  bannerStyle
});

export const handlers = [
  http.get('/api/branding', () => HttpResponse.json({ code: 0, data: branding() })),
  http.post('/api/branding/banner-style', async ({ request }) => {
    const body = (await request.json()) as { style?: string };
    if (body.style !== 'default' && body.style !== 'rainbow') {
      return HttpResponse.json({ code: -1, msg: 'Invalid banner style' });
    }
    bannerStyle = body.style;
    return HttpResponse.json({ code: 0, data: branding() });
  }),
  http.post('/api/auth/login', () => {
    isLoggedIn = true;
    return HttpResponse.json({
      code: 0,
      data: {}
    });
  }),
  http.get('/api/auth/account', () => {
    if (!isLoggedIn) {
      return HttpResponse.json('unauthorized', { status: 401 });
    }
    return HttpResponse.json({
      code: 0,
      data: { username: 'admin', role: 'admin' }
    });
  }),
  http.post('/api/auth/logout', () => {
    isLoggedIn = false;
    return HttpResponse.json({ code: 0 });
  })
];
export const worker = setupWorker(...handlers);
