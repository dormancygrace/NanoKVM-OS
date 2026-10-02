import { createRoot } from 'react-dom/client';

import { Fixture } from './fixture';
import '../../src/i18n';
import '../../src/assets/styles/index.css';

createRoot(document.getElementById('root')!).render(<Fixture />);
