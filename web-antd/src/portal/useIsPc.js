/* =================================================================
 *  判断当前是否用「PC 版」布局
 *  · 宽屏（≥900px）→ PC
 *  · 或者横向屏幕且宽度 ≥640（手机横屏）→ PC
 *  · 手机/平板竖屏 → 移动版（多页面）
 * ================================================================= */
import { useEffect, useState } from 'react';

const QUERY = '(min-width: 900px), (orientation: landscape) and (min-width: 640px)';

export default function useIsPc() {
  const [isPc, setIsPc] = useState(() => {
    try {
      return window.matchMedia(QUERY).matches;
    } catch (e) {
      return true;
    }
  });

  useEffect(() => {
    let mq;
    try {
      mq = window.matchMedia(QUERY);
    } catch (e) {
      return undefined;
    }
    const onChange = (e) => setIsPc(e.matches);
    if (mq.addEventListener) mq.addEventListener('change', onChange);
    else if (mq.addListener) mq.addListener(onChange);
    const onResize = () => setIsPc(mq.matches);
    window.addEventListener('resize', onResize);
    window.addEventListener('orientationchange', onResize);
    onResize();
    return () => {
      if (mq.removeEventListener) mq.removeEventListener('change', onChange);
      else if (mq.removeListener) mq.removeListener(onChange);
      window.removeEventListener('resize', onResize);
      window.removeEventListener('orientationchange', onResize);
    };
  }, []);

  return isPc;
}
