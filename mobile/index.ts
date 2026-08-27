import { registerRootComponent } from 'expo';

import App from './src/App';

// registerRootComponent calls AppRegistry.registerComponent('main', () => App)
// and sets the environment up the same way whether this runs in Expo Go or in
// a native build.
registerRootComponent(App);
