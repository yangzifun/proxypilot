import React from 'react';
import Globe3D from './components/Globe3D';

// three.js 体积大且仅服务器概览使用：单独成包，首屏不加载。
window.__PP_GLOBE3D__ = Globe3D;
window.__PP_GLOBE3D_REACT__ = React;
