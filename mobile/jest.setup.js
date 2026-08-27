// Reanimated ships its own mock; without it every component that animates
// throws in the test environment for reasons unrelated to what is being
// tested.
jest.mock('react-native-reanimated', () =>
  require('react-native-reanimated/mock'),
);
