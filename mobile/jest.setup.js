// Reanimated ships its own mock; without it every component that animates
// throws in the test environment for reasons unrelated to what is being
// tested.
jest.mock('react-native-reanimated', () =>
  require('react-native-reanimated/mock'),
);

// AsyncStorage is a native module and has no implementation in the test
// environment. Its own mock is the supported way to stand it in.
jest.mock('@react-native-async-storage/async-storage', () =>
  require('@react-native-async-storage/async-storage/jest/async-storage-mock'),
);
