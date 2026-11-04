import React, { useState } from 'react';
import { Tabs } from 'antd';
import Nodes from './Nodes';
import Mihomo from './Mihomo';
import MtprotoPage from './MtprotoPage';

export default function TemplateHub() {
  const [tab, setTab] = useState('singbox');
  return (
    <Tabs
      activeKey={tab}
      onChange={setTab}
      items={[
        { key: 'singbox', label: 'singbox', children: <Nodes initialTab="tpl" /> },
        { key: 'mihomo', label: 'mihomo', children: <Mihomo /> },
        { key: 'mtproto', label: 'MTProto', children: <MtprotoPage /> },
      ]}
    />
  );
}
