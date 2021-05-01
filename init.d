#!/bin/bash
#
# PortForwardGo startup script for the server
#
# chkconfig: 2345 90 10
# decription: PortForwardGo Service
#
# Source function library
. /etc/init.d/functions

PIDF=` ps -ef | grep /etc/PortForwardGo | grep -v grep | awk '{print $2}'`

case "$1" in
    
    start)
        if [ ! -n "$PIDF" ];then
            nohup /etc/PortForwardGo/PortForwardGo -config=/etc/PortForwardGo/config.json -log=/etc/PortForwardGo/run.log -certfile=/etc/PortForwardGo/public.pem -certfile=/etc/PortForwardGo/private.key &
            echo "PortForwardGo启动完成"
        else
            echo "PortForwardGo已启动! 请勿重复启动"
        fi
    ;;
    
    stop)
        if [ ! -n "$PIDF" ];then
            echo "PortForwardGo未启动"
        else
            kill -15 $PIDF
            echo "PortForwardGo已经关闭"
            PIDF=""
        fi
    ;;
    
    restart)
        $0 stop
        sleep 5
        $0 start
    ;;
    
    status)
        if [ ! -n "$PIDF" ]
        then
            echo "PortForwardGo未启动"
        else
            echo "PortForwardGo已启动"
        fi
    ;;
    *)
        echo "$0 {start|stop|restart|status}"
    ;;
esac
exit 0