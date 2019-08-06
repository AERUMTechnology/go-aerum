pragma solidity 0.5.10;

contract AtmosGovernance {
    function getComposersWithStakes(uint256 _block, uint256 _timestamp) external view returns (address[] memory, uint256[] memory);
}