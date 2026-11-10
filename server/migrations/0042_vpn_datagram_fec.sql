-- 36.0：数据面双通道与 DATAGRAM 通道 FEC 的配置项
--
-- datagram_enabled：UDP 流量是否走 QUIC DATAGRAM（不被可靠流的队头阻塞拖累）。
--                   关闭后全部走可靠流，等同 35.x 行为。
-- fec_mode：         DATAGRAM 通道的前向纠错模式，off 表示关闭。
-- fec_group：        每组包数，越小越抗丢包、开销越大（1/K）。

ALTER TABLE vpn_settings
    ADD COLUMN datagram_enabled TINYINT(1) NOT NULL DEFAULT 1 AFTER token_ttl_seconds,
    ADD COLUMN fec_mode VARCHAR(16) NOT NULL DEFAULT 'off' AFTER datagram_enabled,
    ADD COLUMN fec_group INT NOT NULL DEFAULT 8 AFTER fec_mode;