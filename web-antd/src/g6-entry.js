import { Graph, register, ExtensionCategory, CanvasEvent } from '@antv/g6';

// G6 仅在流量流向图使用：单独打包，按需加载，避免影响首屏。
window.__PP_G6__ = { Graph, register, ExtensionCategory, CanvasEvent };
