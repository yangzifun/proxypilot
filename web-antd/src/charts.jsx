import * as Svg from './charts-svg';

/**
 * 图表统一走自绘 SVG：
 * 1. Ant Design Charts（G2 5）会额外打入 html2canvas / g-lite / lodash，首屏增加约 1 MB；
 * 2. SVG 版本已是原有降级实现，交互与样式在无 canvas 环境已长期验证；
 * 3. 服务器地图仍使用 Monitor 里的 SVG 定位点，无需 canvas 散点图。
 */
export * from './charts-svg';

export const HAS_CANVAS = false;

/** 兼容旧引用；HAS_CANVAS=false 时不会渲染。 */
export function ScatterPlot() {
  return null;
}
