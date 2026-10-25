const auth = require('./utils/auth')
const offline = require('./utils/offline')

App({
  globalData: {
    online: true,
  },

  onLaunch() {
    this.watchNetwork()
    this.bootstrap()
  },

  onShow() {
    this.bootstrap()
  },

  bootstrap() {
    auth
      .ensureLogin()
      .then(() => offline.flushPending())
      .catch((error) => {
        console.warn('bootstrap login/flush failed', error.code || error.message)
      })
  },

  watchNetwork() {
    wx.getNetworkType({
      success: (result) => {
        this.globalData.online = result.networkType !== 'none'
      },
    })
    wx.onNetworkStatusChange((result) => {
      this.globalData.online = result.isConnected
      if (result.isConnected) {
        this.bootstrap()
      }
    })
  },
})
