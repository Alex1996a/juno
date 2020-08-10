import React, {useEffect, useState} from 'react';
import LeftSide from "./components/LeftSide/index";
import styles from './index.less';
import Editor from "./components/Editor/index";
import {connect} from 'dva';
import ModalSave from "@/pages/app/components/Config/components/ModalSave";
import ModalHistory from "@/pages/app/components/Config/components/ModalHistory";
import EditorMaskLayer from "@/pages/app/components/Config/components/EditorMaskLayer";
import ModalDiff from "@/pages/app/components/Config/components/ModalDiff";

function ConfigEdit(props) {
  const {aid, env, zoneList, zoneCode, appName} = props;

  useEffect(() => {
    if (!appName) return
    if (!env) return

    // reset
    props.dispatch({
      type: 'config/showEditorMaskLayer',
      payload: {
        visible: false,
      }
    })
    props.dispatch({
      type: 'config/setLeftSideActiveMenu',
      payload: 'config-edit'
    })

    // load config-file list
    props.dispatch({
      type: 'config/loadConfigInfo',
      payload: {
        appName,
        env
      }
    });

    props.dispatch({
      type: 'config/setZoneList',
      payload: zoneList
    })

    props.dispatch({
      type: 'config/clearCurrentConfig',
    })
  }, [appName, env, zoneList]);

  useEffect(() => {
    // 从上级拿到数据后写到 "config" model 里面
    props.dispatch({
      type: 'config/setCurrentEnv',
      payload: {
        aid,
        env,
        appName,
        zoneCode
      }
    })
  }, [aid, appName, env, zoneCode])

  return (
    <div className={styles.container}>
      <LeftSide/>

      <div className={styles.main}>
        <EditorMaskLayer/>

        <Editor/>
      </div>

      <ModalSave/>

      <ModalHistory/>

      <ModalDiff/>
    </div>
  );
}

export default connect(() => ({}))(ConfigEdit);
