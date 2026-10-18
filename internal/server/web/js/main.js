import { Api } from './api.js';
import { TelegramWebApp } from './telegram.js';
import { TorreApp } from './components/torre-app.js';

const telegram = new TelegramWebApp();
telegram.start();
document.body.append(new TorreApp(new Api(telegram.initData), telegram));
